package wa

import (
	"archive/zip"
	"context"
	"encoding/hex"
	"fmt"
	"io"
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
			if isWhatsAppAnimatedSticker(message.MediaMimeType) && filepath.Ext(message.MediaLocalPath) != ".json" {
				updated, err := c.extractAndStoreLottieSticker(ctx, message, message.MediaLocalPath)
				if err != nil {
					state.err = err
					return appstore.Message{}, err
				}
				state.message = updated
				c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
				return updated, nil
			}
			state.message = message
			return message, nil
		}
	}
	if len(message.MediaPayload) == 0 {
		state.err = grpcstatus.Error(codes.FailedPrecondition, "media is not available for download")
		return appstore.Message{}, state.err
	}
	if updated, ok, err := c.resolveCachedStickerMedia(ctx, message); err != nil {
		state.err = err
		return appstore.Message{}, err
	} else if ok {
		state.message = updated
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
		return updated, nil
	}

	c.daemon.PublishMediaDownloadChanged(message.ID, message.ChatID, true, "")
	defer func() {
		errorText := ""
		if state.err != nil {
			errorText = state.err.Error()
		}
		c.daemon.PublishMediaDownloadChanged(message.ID, message.ChatID, false, errorText)
	}()

	media, err := downloadableMediaMessage(message)
	if err != nil {
		state.err = err
		return appstore.Message{}, state.err
	}

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || !client.IsConnected() {
		state.err = grpcstatus.Error(codes.Unavailable, "WhatsApp is not connected")
		return appstore.Message{}, state.err
	}

	data, err := client.Download(ctx, media)
	if err != nil {
		state.err = grpcstatus.Errorf(codes.Unavailable, "download media: %v", err)
		return appstore.Message{}, state.err
	}
	if len(data) == 0 || len(data) > maxOutboundMediaBytes {
		state.err = grpcstatus.Errorf(codes.ResourceExhausted, "media size must be between 1 byte and %d MiB", maxOutboundMediaBytes/(1024*1024))
		return appstore.Message{}, state.err
	}

	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", message.ChatID)
	stickerKey, _ := stickerCacheKey(message)
	if message.MediaKind == appstore.MediaKindSticker && stickerKey != "" {
		mediaDir = filepath.Join(c.paths.MediaCacheDir, "stickers")
	}
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		state.err = grpcstatus.Errorf(codes.Internal, "create media cache directory: %v", err)
		return appstore.Message{}, state.err
	}

	fileName := safeMediaFileName(message.ID, mediaExtension(message.MediaMimeType))
	if stickerKey != "" {
		fileName = stickerKey + mediaExtension(message.MediaMimeType)
	}
	localPath := filepath.Join(mediaDir, fileName)
	if err := writeFileAtomic(localPath, data, 0o600); err != nil {
		state.err = grpcstatus.Errorf(codes.Internal, "write media cache file: %v", err)
		return appstore.Message{}, state.err
	}
	if isWhatsAppAnimatedSticker(message.MediaMimeType) {
		localPath, err = extractLottieSticker(localPath)
		if err != nil {
			state.err = err
			return appstore.Message{}, err
		}
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

func (c *Client) ResolveCachedStickerMedia(ctx context.Context, messages []appstore.Message) []appstore.Message {
	for i, message := range messages {
		if message.MediaKind != appstore.MediaKindSticker || message.MediaLocalPath != "" || len(message.MediaPayload) == 0 {
			continue
		}
		updated, ok, err := c.resolveCachedStickerMedia(ctx, message)
		if err != nil {
			c.log.Warnf("Failed to resolve cached sticker media for %s: %v", message.ID, err)
			continue
		}
		if ok {
			messages[i] = updated
		}
	}
	return messages
}

func (c *Client) resolveCachedStickerMedia(ctx context.Context, message appstore.Message) (appstore.Message, bool, error) {
	if message.MediaKind != appstore.MediaKindSticker || len(message.MediaPayload) == 0 {
		return appstore.Message{}, false, nil
	}
	key, err := stickerCacheKey(message)
	if err != nil || key == "" {
		return appstore.Message{}, false, err
	}
	if path := c.existingStickerContentPath(message, key); path != "" {
		updated, err := c.store.UpdateMessageMediaLocalPath(ctx, message.ID, path)
		return updated, err == nil, err
	}
	path, err := c.findDownloadedStickerPath(ctx, message.ID, key)
	if err != nil || path == "" {
		return appstore.Message{}, false, err
	}
	updated, err := c.store.UpdateMessageMediaLocalPath(ctx, message.ID, path)
	return updated, err == nil, err
}

func (c *Client) existingStickerContentPath(message appstore.Message, key string) string {
	if key == "" {
		return ""
	}
	ext := mediaExtension(message.MediaMimeType)
	if isWhatsAppAnimatedSticker(message.MediaMimeType) {
		ext = ".json"
	}
	path := filepath.Join(c.paths.MediaCacheDir, "stickers", key+ext)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func (c *Client) findDownloadedStickerPath(ctx context.Context, messageID, key string) (string, error) {
	messages, err := c.store.ListDownloadedStickerMessages(ctx)
	if err != nil {
		return "", err
	}
	for _, message := range messages {
		if message.ID == messageID || message.MediaLocalPath == "" {
			continue
		}
		otherKey, err := stickerCacheKey(message)
		if err != nil || otherKey != key {
			continue
		}
		if _, err := os.Stat(message.MediaLocalPath); err == nil {
			return message.MediaLocalPath, nil
		}
	}
	return "", nil
}

func stickerCacheKey(message appstore.Message) (string, error) {
	if message.MediaKind != appstore.MediaKindSticker {
		return "", nil
	}
	var sticker waE2E.StickerMessage
	if err := proto.Unmarshal(message.MediaPayload, &sticker); err != nil {
		return "", grpcstatus.Errorf(codes.Internal, "decode sticker metadata: %v", err)
	}
	if hash := sticker.GetFileSHA256(); len(hash) > 0 {
		return hex.EncodeToString(hash), nil
	}
	if hash := sticker.GetFileEncSHA256(); len(hash) > 0 {
		return hex.EncodeToString(hash), nil
	}
	return "", nil
}

func mediaExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "application/was":
		return ".zip"
	case "image/gif":
		return ".gif"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func isWhatsAppAnimatedSticker(mimeType string) bool {
	return strings.EqualFold(strings.TrimSpace(mimeType), "application/was")
}

func (c *Client) extractAndStoreLottieSticker(ctx context.Context, message appstore.Message, archivePath string) (appstore.Message, error) {
	jsonPath, err := extractLottieSticker(archivePath)
	if err != nil {
		return appstore.Message{}, err
	}
	return c.store.UpdateMessageMediaLocalPath(ctx, message.ID, jsonPath)
}

func extractLottieSticker(archivePath string) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", grpcstatus.Errorf(codes.Internal, "open animated sticker archive: %v", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.Name != "animation/animation.json" {
			continue
		}

		opened, err := file.Open()
		if err != nil {
			return "", grpcstatus.Errorf(codes.Internal, "open animated sticker json: %v", err)
		}
		data, err := io.ReadAll(io.LimitReader(opened, maxOutboundMediaBytes+1))
		closeErr := opened.Close()
		if err != nil {
			return "", grpcstatus.Errorf(codes.Internal, "read animated sticker json: %v", err)
		}
		if closeErr != nil {
			return "", grpcstatus.Errorf(codes.Internal, "close animated sticker json: %v", closeErr)
		}
		if len(data) == 0 || len(data) > maxOutboundMediaBytes {
			return "", grpcstatus.Errorf(codes.ResourceExhausted, "animated sticker json size must be between 1 byte and %d MiB", maxOutboundMediaBytes/(1024*1024))
		}

		jsonPath := strings.TrimSuffix(archivePath, filepath.Ext(archivePath)) + ".json"
		if err := writeFileAtomic(jsonPath, data, 0o600); err != nil {
			return "", grpcstatus.Errorf(codes.Internal, "write animated sticker json: %v", err)
		}
		return jsonPath, nil
	}

	return "", grpcstatus.Error(codes.FailedPrecondition, "animated sticker archive does not contain animation.json")
}

func downloadableMediaMessage(message appstore.Message) (interface {
	GetDirectPath() string
	GetURL() string
	GetMediaKey() []byte
	GetFileEncSHA256() []byte
	GetFileSHA256() []byte
	GetFileLength() uint64
}, error) {
	switch message.MediaKind {
	case appstore.MediaKindSticker:
		var sticker waE2E.StickerMessage
		if err := proto.Unmarshal(message.MediaPayload, &sticker); err != nil {
			return nil, grpcstatus.Errorf(codes.Internal, "decode sticker metadata: %v", err)
		}
		if sticker.GetDirectPath() != "" && isPlaceholderMediaURL(sticker.GetURL()) {
			return stickerDownloadable{StickerMessage: &sticker}, nil
		}
		return &sticker, nil
	default:
		var img waE2E.ImageMessage
		if err := proto.Unmarshal(message.MediaPayload, &img); err != nil {
			return nil, grpcstatus.Errorf(codes.Internal, "decode media metadata: %v", err)
		}
		return &img, nil
	}
}

type stickerDownloadable struct {
	*waE2E.StickerMessage
}

func (s stickerDownloadable) GetURL() string {
	return ""
}

func isPlaceholderMediaURL(url string) bool {
	url = strings.TrimSpace(strings.ToLower(url))
	return url == "https://a.whatsapp.net" || url == "http://a.whatsapp.net"
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
