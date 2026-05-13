package wa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	appstore "whatevrd/internal/store"
)

func (c *Client) DownloadMessageMedia(ctx context.Context, messageID string) (appstore.Message, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return appstore.Message{}, grpcstatus.Error(codes.InvalidArgument, "message_id is required")
	}

	c.mediaDownloadMu.Lock()
	if existing := c.mediaDownloads[messageID]; existing != nil {
		done := existing.done
		c.mediaDownloadMu.Unlock()
		select {
		case <-done:
			return existing.message, existing.err
		case <-ctx.Done():
			return appstore.Message{}, ctx.Err()
		}
	}
	state := &mediaDownloadState{done: make(chan struct{})}
	c.mediaDownloads[messageID] = state
	c.mediaDownloadMu.Unlock()

	defer func() {
		c.mediaDownloadMu.Lock()
		delete(c.mediaDownloads, messageID)
		close(state.done)
		c.mediaDownloadMu.Unlock()
	}()

	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		state.err = err
		return appstore.Message{}, err
	}
	if message.MediaLocalPath != "" {
		if _, err := os.Stat(message.MediaLocalPath); err == nil {
			state.message = message
			return message, nil
		}
	}
	if len(message.MediaPayload) == 0 {
		state.err = grpcstatus.Error(codes.FailedPrecondition, "media is not available for download")
		return appstore.Message{}, state.err
	}

	c.daemon.PublishMediaDownloadChanged(message.ID, message.ChatID, true, "")
	defer func() {
		errorText := ""
		if state.err != nil {
			errorText = state.err.Error()
		}
		c.daemon.PublishMediaDownloadChanged(message.ID, message.ChatID, false, errorText)
	}()

	var img waE2E.ImageMessage
	if err := proto.Unmarshal(message.MediaPayload, &img); err != nil {
		state.err = grpcstatus.Errorf(codes.Internal, "decode media metadata: %v", err)
		return appstore.Message{}, state.err
	}

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || !client.IsConnected() {
		state.err = grpcstatus.Error(codes.Unavailable, "WhatsApp is not connected")
		return appstore.Message{}, state.err
	}

	data, err := client.Download(ctx, &img)
	if err != nil {
		state.err = grpcstatus.Errorf(codes.Unavailable, "download media: %v", err)
		return appstore.Message{}, state.err
	}
	if len(data) == 0 || len(data) > maxOutboundMediaBytes {
		state.err = grpcstatus.Errorf(codes.ResourceExhausted, "media size must be between 1 byte and %d MiB", maxOutboundMediaBytes/(1024*1024))
		return appstore.Message{}, state.err
	}

	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", message.ChatID)
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		state.err = grpcstatus.Errorf(codes.Internal, "create media cache directory: %v", err)
		return appstore.Message{}, state.err
	}

	localPath := filepath.Join(mediaDir, safeMediaFileName(message.ID, mediaExtension(message.MediaMimeType)))
	if err := writeFileAtomic(localPath, data, 0o600); err != nil {
		state.err = grpcstatus.Errorf(codes.Internal, "write media cache file: %v", err)
		return appstore.Message{}, state.err
	}

	updated, err := c.store.UpdateMessageMediaLocalPath(ctx, message.ID, localPath)
	if err != nil {
		state.err = err
		return appstore.Message{}, err
	}
	state.message = updated
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	return updated, nil
}

func mediaExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func safeMediaFileName(messageID, extension string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	name := replacer.Replace(strings.TrimSpace(messageID))
	if name == "" {
		name = "media"
	}
	if extension == "" {
		extension = ".bin"
	}
	return fmt.Sprintf("%s%s", name, extension)
}
