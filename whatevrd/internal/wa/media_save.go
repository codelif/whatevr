package wa

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// SaveMediaToPath copies media out of the daemon cache to a destination path
// the caller owns. Exactly one of messageID (a downloaded chat message),
// statusID (a contact status) or avatarJID (a full-resolution profile picture)
// selects the source. Sources without local bytes are fetched first —
// including inbound view-once rows, which keep their keys precisely so an
// explicit save can retrieve them. Nothing here fetches on its own: calling
// this command is the user's deliberate per-item override of "view on your
// phone".
func (c *Client) SaveMediaToPath(ctx context.Context, messageID, statusID, avatarJID, destPath string) (string, error) {
	destPath = strings.TrimSpace(destPath)
	if destPath == "" {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "path is required")
	}
	if !filepath.IsAbs(destPath) {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "destination path must be absolute")
	}
	selectors := 0
	for _, s := range []string{messageID, statusID, avatarJID} {
		if strings.TrimSpace(s) != "" {
			selectors++
		}
	}
	if selectors != 1 {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "exactly one of message_id, status_id or jid is required")
	}

	var src string
	switch {
	case strings.TrimSpace(messageID) != "":
		message, err := c.store.GetMessage(ctx, strings.TrimSpace(messageID))
		if err != nil {
			return "", err
		}
		src, err = c.ensureMessageMediaLocal(ctx, message)
		if err != nil {
			return "", err
		}
	case strings.TrimSpace(statusID) != "":
		status, err := c.store.GetStatusUpdate(ctx, strings.TrimSpace(statusID))
		if err != nil {
			return "", err
		}
		src, err = c.ensureStatusMediaLocal(ctx, status)
		if err != nil {
			return "", err
		}
	default:
		fetched, err := c.FetchProfilePicture(ctx, strings.TrimSpace(avatarJID))
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(fetched) == "" {
			return "", app.NewCommandError(app.CommandErrorNotFound, "no profile picture for this contact")
		}
		src = fetched
	}

	if err := copyFileToDestination(src, destPath); err != nil {
		return "", err
	}
	return destPath, nil
}

// ensureMessageMediaLocal returns a message's cached bytes, downloading them
// first when the row carries keys but no file yet. The download is the same
// explicit fetch the frontend's "Load" button triggers.
func (c *Client) ensureMessageMediaLocal(ctx context.Context, message appstore.Message) (string, error) {
	if strings.TrimSpace(message.MediaLocalPath) != "" {
		if _, err := os.Stat(message.MediaLocalPath); err == nil {
			return message.MediaLocalPath, nil
		}
	}
	if len(message.MediaPayload) == 0 {
		return "", app.NewCommandError(app.CommandErrorRejected, "message has no downloadable media")
	}
	downloaded, err := c.DownloadMessageMedia(ctx, message.ID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(downloaded.MediaLocalPath) == "" {
		return "", app.NewCommandError(app.CommandErrorInternal, "media download completed without a file")
	}
	return downloaded.MediaLocalPath, nil
}

// ensureStatusMediaLocal returns a status's cached bytes, downloading them
// first when the row carries keys but no file yet.
func (c *Client) ensureStatusMediaLocal(ctx context.Context, status appstore.StatusUpdate) (string, error) {
	if strings.TrimSpace(status.MediaLocalPath) != "" {
		if _, err := os.Stat(status.MediaLocalPath); err == nil {
			return status.MediaLocalPath, nil
		}
	}
	if strings.TrimSpace(status.Kind) == "" || strings.TrimSpace(status.Kind) == "text" {
		return "", app.NewCommandError(app.CommandErrorRejected, "status has no media to save")
	}
	if len(status.MediaPayload) == 0 {
		return "", app.NewCommandError(app.CommandErrorRejected, "status media is no longer available")
	}
	downloaded, err := c.downloadStatusMedia(ctx, status)
	if err != nil {
		return "", err
	}
	return downloaded.MediaLocalPath, nil
}

// downloadStatusMedia fetches a status payload into the status cache dir. It
// is the status-table twin of DownloadMessageMedia's core: decode keys,
// Download, verify size, persist the path.
func (c *Client) downloadStatusMedia(ctx context.Context, status appstore.StatusUpdate) (appstore.StatusUpdate, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || !client.IsConnected() {
		return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp is not connected")
	}
	var downloadable whatsmeow.DownloadableMessage
	var senderThumb []byte
	switch status.MediaKind {
	case appstore.MediaKindVideo, appstore.MediaKindGIF:
		video := &waE2E.VideoMessage{}
		if err := proto.Unmarshal(status.MediaPayload, video); err != nil || video.GetDirectPath() == "" {
			return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorRejected, "status media is no longer available")
		}
		downloadable = video
		senderThumb = video.GetJPEGThumbnail()
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		audio := &waE2E.AudioMessage{}
		if err := proto.Unmarshal(status.MediaPayload, audio); err != nil || audio.GetDirectPath() == "" {
			return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorRejected, "status media is no longer available")
		}
		downloadable = audio
	default:
		img := &waE2E.ImageMessage{}
		if err := proto.Unmarshal(status.MediaPayload, img); err != nil || img.GetDirectPath() == "" {
			return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorRejected, "status media is no longer available")
		}
		downloadable = img
		senderThumb = img.GetJPEGThumbnail()
	}

	data, err := client.Download(ctx, downloadable)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	if err := validateInboundMediaSize(int64(len(data))); err != nil {
		return appstore.StatusUpdate{}, err
	}

	mediaDir := filepath.Join(c.paths.MediaCacheDir, "status")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return appstore.StatusUpdate{}, err
	}
	localPath := filepath.Join(mediaDir, safeMediaFileName(status.ID, mediaExtension(status.MediaMimeType)))
	if err := writeFileAtomic(localPath, data, 0o600); err != nil {
		return appstore.StatusUpdate{}, err
	}
	var mediaWidth, mediaHeight int32
	if status.MediaKind == appstore.MediaKindImage {
		mediaWidth, mediaHeight = decodedImageDimensions(data)
	}
	updated, err := c.store.SetStatusMediaPath(ctx, status.ID, localPath, c.saveStatusThumbnail(status.ID, senderThumb), mediaWidth, mediaHeight)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	c.daemon.PublishStatusChanged()
	return updated, nil
}

// saveStatusThumbnail caches a status sender thumbnail (image/video only).
// Empty input returns "" so text/audio rows keep zero values.
func (c *Client) saveStatusThumbnail(statusID string, thumbnail []byte) string {
	if len(thumbnail) == 0 {
		return ""
	}
	mediaDir := filepath.Join(c.paths.MediaCacheDir, "status")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		c.log.Warnf("Failed to create status thumbnail dir for %s: %v", statusID, err)
		return ""
	}
	localPath := filepath.Join(mediaDir, safeMediaFileName(statusID, ".thumb.jpg"))
	if err := writeFileAtomic(localPath, thumbnail, 0o600); err != nil {
		c.log.Warnf("Failed to cache status thumbnail for %s: %v", statusID, err)
		return ""
	}
	return localPath
}

// copyFileToDestination streams src to dest through a temp file + rename, so
// a large video never sits fully in memory and a failed copy never leaves a
// half-written destination. The destination must be absolute and must not be
// a symlink; its parent directory must already exist.
func copyFileToDestination(src, dest string) error {
	if strings.TrimSpace(src) == "" {
		return app.NewCommandError(app.CommandErrorInternal, "media source file is missing")
	}
	in, err := os.Open(src)
	if err != nil {
		return app.NewCommandError(app.CommandErrorNotFound, "media file is not downloaded yet")
	}
	defer in.Close()

	parent := filepath.Dir(dest)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "destination directory does not exist")
	}
	if info, err := os.Lstat(dest); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return app.NewCommandError(app.CommandErrorInvalidArgument, "destination must not be a symlink")
		}
		if !info.Mode().IsRegular() {
			return app.NewCommandError(app.CommandErrorInvalidArgument, "destination must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "destination is not accessible")
	}

	tmp, err := os.CreateTemp(parent, "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return app.NewCommandError(app.CommandErrorInternal, "create destination file: %v", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return app.NewCommandError(app.CommandErrorInternal, "secure destination file: %v", err)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return app.NewCommandError(app.CommandErrorInternal, "write destination file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return app.NewCommandError(app.CommandErrorInternal, "write destination file: %v", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return app.NewCommandError(app.CommandErrorInternal, "save destination file: %v", err)
	}
	return nil
}
