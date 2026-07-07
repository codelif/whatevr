package wa

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

const (
	mediaRetryTimeout = 30 * time.Second
	maxInt32          = 1<<31 - 1
)

type downloadableMedia interface {
	GetDirectPath() string
	GetURL() string
	GetMediaKey() []byte
	GetFileEncSHA256() []byte
	GetFileSHA256() []byte
	GetFileLength() uint64
}

func (c *Client) DownloadMessageMedia(ctx context.Context, messageID string) (appstore.Message, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return appstore.Message{}, app.NewCommandError(app.CommandErrorInvalidArgument, "message_id is required")
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
		state.err = app.NewCommandError(app.CommandErrorRejected, "media is not available for download")
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

	media, err := downloadableMediaMessage(message)
	if err != nil {
		state.err = err
		return appstore.Message{}, state.err
	}

	totalBytes := media.GetFileLength()
	if totalBytes > maxOutboundMediaBytes {
		state.err = app.NewCommandError(app.CommandErrorRejected, "media size must be between 1 byte and %d MiB", maxOutboundMediaBytes/(1024*1024))
		return appstore.Message{}, state.err
	}

	if message.MediaDownloadError != "" {
		updated, err := c.store.SetMessageMediaDownloadError(ctx, message.ID, "")
		if err != nil {
			state.err = err
			return appstore.Message{}, err
		}
		message = updated
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	}

	c.daemon.PublishMediaDownloadChanged(message.ID, message.ChatID, true, "", 0, totalBytes)
	defer func() {
		errorText := ""
		if state.err != nil {
			errorText = state.err.Error()
		}
		c.daemon.PublishMediaDownloadChanged(message.ID, message.ChatID, false, errorText, 0, totalBytes)
		if errorText != "" {
			updated, err := c.store.SetMessageMediaDownloadError(context.Background(), message.ID, errorText)
			if err != nil {
				c.log.Errorf("Persist media download error for %s: %v", message.ID, err)
				return
			}
			c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
		}
	}()

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || !client.IsConnected() {
		state.err = app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp is not connected")
		return appstore.Message{}, state.err
	}

	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", message.ChatID)
	stickerKey, _ := stickerCacheKey(message)
	if message.MediaKind == appstore.MediaKindSticker && stickerKey != "" {
		mediaDir = filepath.Join(c.paths.MediaCacheDir, "stickers")
	}
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		state.err = app.NewCommandError(app.CommandErrorInternal, "create media cache directory: %v", err)
		return appstore.Message{}, state.err
	}

	fileName := safeMediaFileName(message.ID, mediaExtension(message.MediaMimeType))
	if stickerKey != "" {
		fileName = stickerKey + mediaExtension(message.MediaMimeType)
	}
	localPath := filepath.Join(mediaDir, fileName)
	partPath := localPath + ".part"

	partFile, err := os.OpenFile(partPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		state.err = app.NewCommandError(app.CommandErrorInternal, "create media cache file: %v", err)
		return appstore.Message{}, state.err
	}
	partOpen := true
	defer func() {
		if partOpen {
			partFile.Close()
		}
		if state.err != nil {
			os.Remove(partPath)
		}
	}()

	progress := &mediaProgressFile{
		File: partFile,
		report: func(receivedBytes uint64) {
			c.daemon.PublishMediaDownloadChanged(message.ID, message.ChatID, true, "", receivedBytes, totalBytes)
		},
	}
	if err := client.DownloadToFile(ctx, media, progress); err != nil {
		if staleMediaDownloadError(err) {
			message, err = c.refreshMediaForDownload(ctx, client, message, media)
			if err != nil {
				state.err = err
				return appstore.Message{}, state.err
			}
			media, err = downloadableMediaMessage(message)
			if err != nil {
				state.err = err
				return appstore.Message{}, state.err
			}
			if _, err = partFile.Seek(0, io.SeekStart); err == nil {
				if err = partFile.Truncate(0); err == nil {
					progress.position = 0
					err = client.DownloadToFile(ctx, media, progress)
				}
			}
		}
		if err != nil {
			state.err = app.NewCommandError(app.CommandErrorNotConnected, "download media: %v", err)
			return appstore.Message{}, state.err
		}
	}

	fileInfo, err := partFile.Stat()
	if err != nil {
		state.err = app.NewCommandError(app.CommandErrorInternal, "stat media cache file: %v", err)
		return appstore.Message{}, state.err
	}
	if fileInfo.Size() == 0 || fileInfo.Size() > maxOutboundMediaBytes {
		state.err = app.NewCommandError(app.CommandErrorRejected, "media size must be between 1 byte and %d MiB", maxOutboundMediaBytes/(1024*1024))
		return appstore.Message{}, state.err
	}
	var mediaWidth, mediaHeight int32
	if message.MediaKind == appstore.MediaKindImage {
		if _, err := partFile.Seek(0, io.SeekStart); err == nil {
			mediaWidth, mediaHeight = decodedImageDimensionsFromReader(partFile)
		}
	}
	if err := partFile.Close(); err != nil {
		partOpen = false
		state.err = app.NewCommandError(app.CommandErrorInternal, "close media cache file: %v", err)
		return appstore.Message{}, state.err
	}
	partOpen = false
	if err := os.Rename(partPath, localPath); err != nil {
		state.err = app.NewCommandError(app.CommandErrorInternal, "store media cache file: %v", err)
		return appstore.Message{}, state.err
	}
	if isWhatsAppAnimatedSticker(message.MediaMimeType) {
		localPath, err = extractLottieSticker(localPath)
		if err != nil {
			state.err = err
			return appstore.Message{}, err
		}
	}

	updated, err := c.store.UpdateMessageMediaLocalPathWithDimensions(ctx, message.ID, localPath, mediaWidth, mediaHeight)
	if err != nil {
		state.err = err
		return appstore.Message{}, err
	}
	state.message = updated
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	return updated, nil
}

// mediaProgressFile wraps the media cache file handed to whatsmeow's
// DownloadToFile. The HTTP body is streamed in through sequential Write calls,
// so counting those gives live download progress; the later in-place
// decryption pass uses WriteAt and is intentionally not counted. Seeks reset
// the counter so whatsmeow's rewind-and-retry path restarts the progress
// honestly instead of double-counting.
type mediaProgressFile struct {
	*os.File
	position   int64
	lastReport time.Time
	lastSeen   uint64
	report     func(receivedBytes uint64)
}

const mediaProgressReportInterval = 150 * time.Millisecond

func (p *mediaProgressFile) Write(data []byte) (int, error) {
	n, err := p.File.Write(data)
	p.position += int64(n)
	if p.report != nil && p.position > 0 {
		received := uint64(p.position)
		now := time.Now()
		if received != p.lastSeen && now.Sub(p.lastReport) >= mediaProgressReportInterval {
			p.lastReport = now
			p.lastSeen = received
			p.report(received)
		}
	}
	return n, err
}

// ReadFrom must be overridden: io.Copy prefers the destination's ReaderFrom,
// and the one promoted from the embedded *os.File would stream the whole HTTP
// body kernel-side without ever hitting Write — no progress would be reported.
// Looping through Write keeps the counting (and its throttling) in one place.
func (p *mediaProgressFile) ReadFrom(reader io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			written, writeErr := p.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written < n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func (p *mediaProgressFile) Seek(offset int64, whence int) (int64, error) {
	pos, err := p.File.Seek(offset, whence)
	if err == nil {
		p.position = pos
	}
	return pos, err
}

func decodedImageDimensions(data []byte) (int32, int32) {
	return decodedImageDimensionsFromReader(bytes.NewReader(data))
}

func decodedImageDimensionsFromReader(reader io.Reader) (int32, int32) {
	cfg, _, err := image.DecodeConfig(reader)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxInt32 || cfg.Height > maxInt32 {
		return 0, 0
	}
	return int32(cfg.Width), int32(cfg.Height)
}

func staleMediaDownloadError(err error) bool {
	return errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403) ||
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) ||
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410)
}

func (c *Client) refreshMediaForDownload(ctx context.Context, client *whatsmeow.Client, message appstore.Message, media downloadableMedia) (appstore.Message, error) {
	info, err := mediaRetryMessageInfo(message)
	if err != nil {
		return appstore.Message{}, err
	}
	mediaKey := append([]byte(nil), media.GetMediaKey()...)
	if len(mediaKey) == 0 {
		return appstore.Message{}, app.NewCommandError(app.CommandErrorRejected, "media retry is missing media key")
	}

	state := &mediaRetryState{done: make(chan struct{}), mediaKey: mediaKey}
	c.mediaRetryMu.Lock()
	if c.mediaRetries == nil {
		c.mediaRetries = make(map[string]*mediaRetryState)
	}
	if existing := c.mediaRetries[message.ID]; existing != nil {
		state = existing
	} else {
		c.mediaRetries[message.ID] = state
	}
	c.mediaRetryMu.Unlock()
	defer c.removeMediaRetryState(message.ID, state)

	if err := client.SendMediaRetryReceipt(ctx, info, mediaKey); err != nil {
		return appstore.Message{}, app.NewCommandError(app.CommandErrorNotConnected, "request media retry: %v", err)
	}
	directPath, err := waitMediaRetry(ctx, state)
	if err != nil {
		return appstore.Message{}, err
	}

	payload, err := refreshedMediaPayload(message, directPath)
	if err != nil {
		return appstore.Message{}, err
	}
	updated, err := c.store.UpdateMessageMediaPayload(ctx, message.ID, payload)
	if err != nil {
		return appstore.Message{}, err
	}
	return updated, nil
}

func (c *Client) removeMediaRetryState(messageID string, state *mediaRetryState) {
	c.mediaRetryMu.Lock()
	if c.mediaRetries[messageID] == state {
		delete(c.mediaRetries, messageID)
	}
	c.mediaRetryMu.Unlock()
}

func waitMediaRetry(ctx context.Context, state *mediaRetryState) (string, error) {
	timer := time.NewTimer(mediaRetryTimeout)
	defer timer.Stop()

	select {
	case <-state.done:
		if state.err != nil {
			return "", state.err
		}
		if state.directPath == "" {
			return "", app.NewCommandError(app.CommandErrorRejected, "media retry response did not include a download path")
		}
		return state.directPath, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", app.NewCommandError(app.CommandErrorNotConnected, "media retry response timed out")
	}
}

func (c *Client) handleMediaRetry(ctx context.Context, evt *events.MediaRetry) {
	if evt == nil || evt.MessageID == "" || evt.ChatID.IsEmpty() {
		return
	}
	chatIDs := []string{evt.ChatID.String()}
	if normalized := c.normalizeJIDForChat(ctx, evt.ChatID).String(); normalized != "" && normalized != chatIDs[0] {
		chatIDs = append(chatIDs, normalized)
	}

	var messageID string
	var state *mediaRetryState
	c.mediaRetryMu.Lock()
	for _, chatID := range chatIDs {
		candidateID := internalMessageIDForChat(chatID, evt.MessageID)
		candidate := c.mediaRetries[candidateID]
		if candidate != nil && !candidate.completed {
			messageID = candidateID
			state = candidate
			break
		}
	}
	if state == nil {
		c.mediaRetryMu.Unlock()
		return
	}
	mediaKey := append([]byte(nil), state.mediaKey...)
	c.mediaRetryMu.Unlock()

	notif, err := whatsmeow.DecryptMediaRetryNotification(evt, mediaKey)
	if err != nil {
		c.completeMediaRetry(messageID, "", mediaRetryEventError(err))
		return
	}
	if notif.GetResult() != waMmsRetry.MediaRetryNotification_SUCCESS {
		c.completeMediaRetry(messageID, "", app.NewCommandError(app.CommandErrorRejected, "media retry failed with result %s", notif.GetResult().String()))
		return
	}
	c.completeMediaRetry(messageID, notif.GetDirectPath(), nil)
}

func (c *Client) completeMediaRetry(messageID, directPath string, err error) {
	c.mediaRetryMu.Lock()
	state := c.mediaRetries[messageID]
	if state == nil || state.completed {
		c.mediaRetryMu.Unlock()
		return
	}
	state.directPath = directPath
	state.err = err
	state.completed = true
	close(state.done)
	c.mediaRetryMu.Unlock()
}

func mediaRetryEventError(err error) error {
	if errors.Is(err, whatsmeow.ErrMediaNotAvailableOnPhone) {
		return app.NewCommandError(app.CommandErrorNotFound, "media is no longer available on WhatsApp")
	}
	return app.NewCommandError(app.CommandErrorNotConnected, "media retry response: %v", err)
}

func mediaRetryMessageInfo(message appstore.Message) (*types.MessageInfo, error) {
	chat, err := types.ParseJID(message.ChatID)
	if err != nil {
		return nil, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid media chat ID: %v", err)
	}
	externalID := strings.TrimSpace(appstore.ExternalMessageID(message.ChatID, message.ID))
	if externalID == "" {
		return nil, app.NewCommandError(app.CommandErrorInvalidArgument, "message ID is required")
	}

	fromMe := message.Direction == appstore.DirectionOutgoing
	isGroup := chat.Server == types.GroupServer
	var sender types.JID
	if isGroup && !fromMe {
		senderID := strings.TrimSpace(message.SenderID)
		if senderID == "" {
			return nil, app.NewCommandError(app.CommandErrorRejected, "media retry is missing group sender")
		}
		sender, err = types.ParseJID(senderID)
		if err != nil {
			return nil, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid media sender ID: %v", err)
		}
	}

	return &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chat,
			Sender:   sender,
			IsFromMe: fromMe,
			IsGroup:  isGroup,
		},
		ID: types.MessageID(externalID),
	}, nil
}

func refreshedMediaPayload(message appstore.Message, directPath string) ([]byte, error) {
	directPath = strings.TrimSpace(directPath)
	if directPath == "" {
		return nil, app.NewCommandError(app.CommandErrorRejected, "media retry response did not include a download path")
	}

	switch message.MediaKind {
	case appstore.MediaKindSticker:
		var sticker waE2E.StickerMessage
		if err := proto.Unmarshal(message.MediaPayload, &sticker); err != nil {
			return nil, app.NewCommandError(app.CommandErrorInternal, "decode sticker metadata: %v", err)
		}
		sticker.DirectPath = proto.String(directPath)
		sticker.URL = nil
		payload, err := proto.Marshal(&sticker)
		if err != nil {
			return nil, app.NewCommandError(app.CommandErrorInternal, "encode sticker metadata: %v", err)
		}
		return payload, nil
	default:
		var img waE2E.ImageMessage
		if err := proto.Unmarshal(message.MediaPayload, &img); err != nil {
			return nil, app.NewCommandError(app.CommandErrorInternal, "decode media metadata: %v", err)
		}
		img.DirectPath = proto.String(directPath)
		img.URL = nil
		payload, err := proto.Marshal(&img)
		if err != nil {
			return nil, app.NewCommandError(app.CommandErrorInternal, "encode media metadata: %v", err)
		}
		return payload, nil
	}
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

// findDownloadedStickerPath resolves a sticker's content via the indexed
// cache-key lookup. Cache keys are populated on insert and backfilled for
// legacy rows at startup (ensureStickerCacheKeys), so no full-table fallback
// scan is needed.
func (c *Client) findDownloadedStickerPath(ctx context.Context, messageID, key string) (string, error) {
	path, err := c.store.DownloadedStickerPathByCacheKey(ctx, messageID, key)
	if err != nil || path == "" {
		return "", err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return "", nil
	}
	return path, nil
}

func stickerCacheKey(message appstore.Message) (string, error) {
	if message.MediaKind != appstore.MediaKindSticker {
		return "", nil
	}
	if message.MediaCacheKey != "" {
		return message.MediaCacheKey, nil
	}
	key, err := appstore.StickerCacheKeyFromPayload(message.MediaPayload)
	if err != nil {
		return "", app.NewCommandError(app.CommandErrorInternal, "decode sticker metadata: %v", err)
	}
	return key, nil
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

// isAnimatedWebP reports whether a WebP file is animated, by inspecting the
// RIFF/VP8X header. WhatsApp's library metadata (recents, favorites, pack
// items) carries no animation flag and animated/static WebP share the
// image/webp mimetype, so the bytes are the only reliable signal — without
// this, animated stickers sent from the library render as a still frame on the
// sender's own side even though the recipient sees them animate.
func isAnimatedWebP(data []byte) bool {
	// Layout: "RIFF" <size> "WEBP" "VP8X" <chunk size> <flags byte>...
	// The animation bit lives in the VP8X flags byte at offset 20 (0x02). A
	// simple (non-extended) WebP has no VP8X chunk and is always a still frame.
	if len(data) < 21 {
		return false
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	if string(data[12:16]) != "VP8X" {
		return false
	}
	return data[20]&0x02 != 0
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
		return "", app.NewCommandError(app.CommandErrorInternal, "open animated sticker archive: %v", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.Name != "animation/animation.json" {
			continue
		}

		opened, err := file.Open()
		if err != nil {
			return "", app.NewCommandError(app.CommandErrorInternal, "open animated sticker json: %v", err)
		}
		data, err := io.ReadAll(io.LimitReader(opened, maxOutboundMediaBytes+1))
		closeErr := opened.Close()
		if err != nil {
			return "", app.NewCommandError(app.CommandErrorInternal, "read animated sticker json: %v", err)
		}
		if closeErr != nil {
			return "", app.NewCommandError(app.CommandErrorInternal, "close animated sticker json: %v", closeErr)
		}
		if len(data) == 0 || len(data) > maxOutboundMediaBytes {
			return "", app.NewCommandError(app.CommandErrorRejected, "animated sticker json size must be between 1 byte and %d MiB", maxOutboundMediaBytes/(1024*1024))
		}

		jsonPath := strings.TrimSuffix(archivePath, filepath.Ext(archivePath)) + ".json"
		if err := writeFileAtomic(jsonPath, data, 0o600); err != nil {
			return "", app.NewCommandError(app.CommandErrorInternal, "write animated sticker json: %v", err)
		}
		return jsonPath, nil
	}

	return "", app.NewCommandError(app.CommandErrorRejected, "animated sticker archive does not contain animation.json")
}

func downloadableMediaMessage(message appstore.Message) (downloadableMedia, error) {
	switch message.MediaKind {
	case appstore.MediaKindSticker:
		var sticker waE2E.StickerMessage
		if err := proto.Unmarshal(message.MediaPayload, &sticker); err != nil {
			return nil, app.NewCommandError(app.CommandErrorInternal, "decode sticker metadata: %v", err)
		}
		if sticker.GetDirectPath() != "" && isPlaceholderMediaURL(sticker.GetURL()) {
			return stickerDownloadable{StickerMessage: &sticker}, nil
		}
		return &sticker, nil
	default:
		var img waE2E.ImageMessage
		if err := proto.Unmarshal(message.MediaPayload, &img); err != nil {
			return nil, app.NewCommandError(app.CommandErrorInternal, "decode media metadata: %v", err)
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
