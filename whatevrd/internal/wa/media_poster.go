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

// videoPosterPath names the derived poster. The version suffix is what makes an
// improvement to the extraction below reach clips that already have a poster:
// a stored thumbnail path no longer matching this one is exactly the condition
// deriveAndPublishVideoPoster treats as "not done yet", so the backfill worker
// re-derives each video once and nothing else has to know.
//
// v2 replaced a literal frame 0, which is black on a large fraction of what
// WhatsApp carries.
func videoPosterPath(mediaPath string) string {
	return mediaPath + ".poster2.jpg"
}

// supersededVideoPosterPaths lists the outputs earlier versions wrote, so a
// re-derive does not leave them on disk forever.
func supersededVideoPosterPaths(mediaPath string) []string {
	return []string{mediaPath + ".poster.jpg"}
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
	// Only once the row points at the new file: a crash between the two would
	// otherwise leave the message naming a poster that has been deleted.
	for _, stale := range supersededVideoPosterPaths(message.MediaLocalPath) {
		if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
			c.log.Debugf("Could not remove superseded poster %s: %v", stale, err)
		}
	}
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
}

// posterSeekSeconds is how far in to start looking for a representative frame.
// Videos routinely open on a fade from black, a slate, or a single dark frame,
// and a poster taken from there says nothing about the clip.
const posterSeekSeconds = "1"

// posterScale fits the frame inside 1280 on both axes without ever upscaling.
const posterScale = "scale='min(1280,iw)':'min(1280,ih)':force_original_aspect_ratio=decrease"

// extractVideoPoster picks a representative frame, scales it without upscaling
// so neither dimension exceeds 1280 pixels, and atomically publishes a JPEG.
//
// The frame is chosen by ffmpeg's `thumbnail` filter, which scores a batch of
// frames against their average and keeps the least typical one, after seeking
// past the opening. Taking frame 0 instead, which is what this did, produced a
// black poster for every clip that fades in, and those bubbles looked broken.
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

	// Two attempts, in preference order. The first can come up empty on a clip
	// shorter than the seek, and `thumbnail` needs a batch of frames it may not
	// have; the fallback is the old behaviour, which always produces something.
	attempts := [][]string{
		{
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
			"-ss", posterSeekSeconds,
			"-i", sourcePath,
			"-vf", "thumbnail=100," + posterScale,
			"-frames:v", "1", "-q:v", "2", "-f", "image2", tmpPath,
		},
		{
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
			"-i", sourcePath,
			"-vf", posterScale,
			"-frames:v", "1", "-q:v", "2", "-f", "image2", tmpPath,
		},
	}

	var lastOutput []byte
	for i, args := range attempts {
		lastOutput, err = exec.CommandContext(decodeCtx, ffmpeg, args...).CombinedOutput()
		if err == nil {
			// ffmpeg exits 0 having written nothing when the seek landed past
			// the end of a short clip, so the file is the real answer.
			if info, statErr := os.Stat(tmpPath); statErr == nil && info.Size() > 0 {
				break
			}
			if i < len(attempts)-1 {
				continue
			}
			return fmt.Errorf("ffmpeg poster extraction produced no frame")
		}
		if decodeCtx.Err() != nil {
			return decodeCtx.Err()
		}
		if i == len(attempts)-1 {
			return fmt.Errorf("ffmpeg poster extraction: %w: %s", err, strings.TrimSpace(string(lastOutput)))
		}
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
