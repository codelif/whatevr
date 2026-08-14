package wa

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	appstore "whatevrd/internal/store"
)

const posterDecodeTimeout = 20 * time.Second

type posterPriority uint8

const (
	posterPriorityBackfill posterPriority = iota + 1
	posterPriorityDownload
)

// queueVideoPoster adds work to the daemon's single poster decoder. Completed
// downloads use the high-priority queue so startup backfill cannot delay media
// the user is currently watching.
func (c *Client) queueVideoPoster(message appstore.Message, priority posterPriority) {
	if message.MediaKind != appstore.MediaKindVideo && message.MediaKind != appstore.MediaKindGIF {
		return
	}
	if strings.TrimSpace(message.MediaLocalPath) == "" {
		return
	}

	c.posterMu.Lock()
	if c.posterQueued == nil {
		c.posterQueued = make(map[string]posterPriority)
	}
	if queued := c.posterQueued[message.ID]; queued >= priority {
		c.posterMu.Unlock()
		return
	}
	c.posterQueued[message.ID] = priority
	if priority == posterPriorityDownload {
		c.posterHigh = append(c.posterHigh, message)
	} else {
		c.posterLow = append(c.posterLow, message)
	}
	if c.posterWake == nil {
		c.posterWake = make(chan struct{}, 1)
	}
	wake := c.posterWake
	c.posterMu.Unlock()

	select {
	case wake <- struct{}{}:
	default:
	}
}

func (c *Client) takeVideoPoster() (appstore.Message, bool) {
	c.posterMu.Lock()
	defer c.posterMu.Unlock()

	for len(c.posterHigh) > 0 {
		message := c.posterHigh[0]
		c.posterHigh = c.posterHigh[1:]
		if c.posterQueued[message.ID] != posterPriorityDownload {
			continue
		}
		delete(c.posterQueued, message.ID)
		return message, true
	}
	for len(c.posterLow) > 0 {
		message := c.posterLow[0]
		c.posterLow = c.posterLow[1:]
		if c.posterQueued[message.ID] != posterPriorityBackfill {
			continue
		}
		delete(c.posterQueued, message.ID)
		return message, true
	}
	return appstore.Message{}, false
}

func (c *Client) runVideoPosterWorker(ctx context.Context) {
	// Listing is read-only and happens on this background goroutine. Startup and
	// frontend subscriptions do not wait for it.
	candidates, err := c.store.ListVideoPosterCandidates(ctx)
	if err != nil {
		c.log.Warnf("Failed to list video poster candidates: %v", err)
	} else {
		for _, message := range candidates {
			c.queueVideoPoster(message, posterPriorityBackfill)
		}
	}

	for {
		if message, ok := c.takeVideoPoster(); ok {
			c.deriveAndPublishVideoPoster(ctx, message)
			continue
		}

		c.posterMu.Lock()
		wake := c.posterWake
		c.posterMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

func videoPosterPath(mediaPath string) string {
	return mediaPath + ".poster.jpg"
}

func (c *Client) deriveAndPublishVideoPoster(ctx context.Context, message appstore.Message) {
	posterPath := videoPosterPath(message.MediaLocalPath)
	if message.MediaThumbnailLocalPath == posterPath {
		if info, err := os.Stat(posterPath); err == nil && info.Mode().IsRegular() {
			return
		}
	}

	// A completed atomic output may exist after a crash between rename and the
	// database update. Reuse it instead of decoding the video again.
	if info, err := os.Stat(posterPath); err != nil || !info.Mode().IsRegular() {
		extractor := c.posterExtractor
		if extractor == nil {
			extractor = extractVideoPoster
		}
		if err := extractor(ctx, message.MediaLocalPath, posterPath); err != nil {
			c.log.Debugf("Could not derive video poster for %s: %v", message.ID, err)
			return
		}
	}

	updated, err := c.store.UpdateMessageMediaThumbnailLocalPath(ctx, message.ID, posterPath)
	if err != nil {
		c.log.Warnf("Failed to store video poster for %s: %v", message.ID, err)
		return
	}
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
}

// extractVideoPoster decodes the first frame, scales it without upscaling so
// neither dimension exceeds 1280 pixels, and atomically publishes a JPEG.
func extractVideoPoster(ctx context.Context, sourcePath, outputPath string) error {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return err
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return err
	}

	decodeCtx, cancel := context.WithTimeout(ctx, posterDecodeTimeout)
	defer cancel()

	tmp, err := os.CreateTemp(filepath.Dir(outputPath), "."+filepath.Base(outputPath)+"-*")
	if err != nil {
		return fmt.Errorf("create poster temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close poster temporary file: %w", err)
	}
	defer os.Remove(tmpPath)

	output, err := exec.CommandContext(decodeCtx, ffmpeg,
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-i", sourcePath,
		"-vf", "scale='min(1280,iw)':'min(1280,ih)':force_original_aspect_ratio=decrease",
		"-frames:v", "1",
		"-q:v", "2",
		"-f", "image2",
		tmpPath,
	).CombinedOutput()
	if err != nil {
		if decodeCtx.Err() != nil {
			return decodeCtx.Err()
		}
		return fmt.Errorf("ffmpeg poster extraction: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("set poster permissions: %w", err)
	}
	file, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open completed poster: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync completed poster: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close completed poster: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("publish completed poster: %w", err)
	}
	return nil
}
