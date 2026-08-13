package wa

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

const (
	maxOutboundMediaBytes = 25 * 1024 * 1024
	maxPinnedChats        = 3
)

type readBatch struct {
	sender     types.JID
	messageIDs []types.MessageID
}

func (c *Client) SendText(ctx context.Context, chatID, text, replyToMessageID string, mentionedJIDs []string) (appstore.SavedTextMessage, error) {
	rpcArrival := time.Now()
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "text is required")
	}

	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()
	replyTo, err := c.replySnapshotForSend(ctx, chatID, replyToMessageID)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	messageID := client.GenerateMessageID()
	saved, err := c.store.SaveTextMessage(ctx, appstore.TextMessageInput{
		ID:          internalMessageIDForChat(chatID, messageID),
		ChatID:      chatID,
		ChatName:    "",
		SenderID:    "me",
		Text:        trimmedText,
		Timestamp:   time.Now(),
		Direction:   appstore.DirectionOutgoing,
		Status:      appstore.StatusPending,
		IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
		CountUnread: false,
		ReplyTo:     replyTo,
		Mentions:    c.resolveMentions(ctx, mentionedJIDs),
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	if saved.Inserted {
		c.beginSendTiming(saved.Message.ID, rpcArrival)
		c.log.Infof("Queued text message %s to %s", saved.Message.ID, chatID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID}, avatarPriorityVisible)
	c.signalSendQueue()
	return saved, nil
}

func (c *Client) SetChatPresence(ctx context.Context, chatID string, composing bool) error {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil
	}

	jid, err := types.ParseJID(chatID)
	if err != nil {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}

	state := types.ChatPresencePaused
	if composing {
		state = types.ChatPresenceComposing
	}

	return client.SendChatPresence(ctx, jid, state, types.ChatPresenceMediaText)
}

func (c *Client) SubscribeChatPresence(ctx context.Context, chatID string) error {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil
	}

	jid, err := types.ParseJID(chatID)
	if err != nil {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	if jid.Server == types.GroupServer {
		return nil
	}

	if err := client.SendPresence(ctx, types.PresenceAvailable); err != nil {
		return err
	}
	return client.SubscribePresence(ctx, jid)
}

func (c *Client) SendMedia(ctx context.Context, chatID, filePath, caption, replyToMessageID string) (appstore.SavedTextMessage, error) {
	return c.SendMediaWithMentions(ctx, chatID, filePath, caption, replyToMessageID, nil)
}

func (c *Client) SendMediaWithMentions(ctx context.Context, chatID, filePath, caption, replyToMessageID string, mentionedJIDs []string) (appstore.SavedTextMessage, error) {
	rpcArrival := time.Now()
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	}

	data, mimeType, extension, err := readOutboundMedia(filePath)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()
	replyTo, err := c.replySnapshotForSend(ctx, chatID, replyToMessageID)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	messageID := client.GenerateMessageID()
	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", chatID)
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return appstore.SavedTextMessage{}, err
	}
	localPath := filepath.Join(mediaDir, fmt.Sprintf("%s%s", safeFilenamePart(string(messageID)), extension))
	if err := writeFileAtomic(localPath, data, 0o600); err != nil {
		return appstore.SavedTextMessage{}, err
	}

	var mediaWidth, mediaHeight int32
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		mediaWidth = int32(cfg.Width)
		mediaHeight = int32(cfg.Height)
	}

	saved, err := c.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, messageID),
			ChatID:      chatID,
			SenderID:    "me",
			Text:        caption,
			Timestamp:   time.Now(),
			Direction:   appstore.DirectionOutgoing,
			Status:      appstore.StatusPending,
			IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
			CountUnread: false,
			ReplyTo:     replyTo,
			Mentions:    c.resolveMentions(ctx, mentionedJIDs),
		},
		MediaKind:      appstore.MediaKindImage,
		MediaMimeType:  mimeType,
		MediaLocalPath: localPath,
		MediaWidth:     mediaWidth,
		MediaHeight:    mediaHeight,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	if saved.Inserted {
		c.beginSendTiming(saved.Message.ID, rpcArrival)
		c.log.Infof("Queued media message %s to %s", saved.Message.ID, chatID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID}, avatarPriorityVisible)
	c.signalSendQueue()
	return saved, nil
}

func (c *Client) replySnapshotForSend(ctx context.Context, chatID, replyToMessageID string) (appstore.MessageReply, error) {
	replyToMessageID = strings.TrimSpace(replyToMessageID)
	if replyToMessageID == "" {
		return appstore.MessageReply{}, nil
	}

	messageID := replyToMessageID
	if !strings.HasPrefix(messageID, chatID+":") {
		messageID = internalMessageIDForChat(chatID, types.MessageID(messageID))
	}
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appstore.MessageReply{}, app.NewCommandError(app.CommandErrorNotFound, "reply message not found")
		}
		return appstore.MessageReply{}, err
	}
	if message.ChatID != chatID {
		return appstore.MessageReply{}, app.NewCommandError(app.CommandErrorInvalidArgument, "reply message is not in this chat")
	}

	return replyFromStoredMessage(message), nil
}

func replyFromStoredMessage(message appstore.Message) appstore.MessageReply {
	return appstore.MessageReply{
		MessageID:     message.ID,
		SenderID:      message.SenderID,
		SenderName:    message.SenderName,
		Text:          message.Text,
		MediaKind:     message.MediaKind,
		MediaMimeType: message.MediaMimeType,
		Direction:     message.Direction,
	}
}

func readOutboundMedia(filePath string) ([]byte, string, string, error) {
	if !filepath.IsAbs(filePath) {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file path must be absolute")
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file is not accessible")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be a regular file")
	}
	if info.Size() <= 0 {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file is empty")
	}
	if info.Size() > maxOutboundMediaBytes {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be <= %d MiB", maxOutboundMediaBytes/(1024*1024))
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, "", "", app.NewCommandError(app.CommandErrorRejected, "media file owner is not allowed")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file is not readable")
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxOutboundMediaBytes+1))
	if err != nil {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file could not be read")
	}
	if len(data) > maxOutboundMediaBytes {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be <= %d MiB", maxOutboundMediaBytes/(1024*1024))
	}
	mimeType := http.DetectContentType(data)
	// WhatsApp GIFs are short MP4 videos with a gif-playback flag; sending the
	// raw .gif bytes through the image path delivers a static picture. Refuse
	// until a GIF→video transcode path exists.
	if mimeType == "image/gif" {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "GIF files can't be sent yet: WhatsApp treats GIFs as short videos, which whatevr does not support sending")
	}
	extension, ok := outboundImageExtension(mimeType)
	if !ok {
		return nil, "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be a supported image")
	}
	return data, mimeType, extension, nil
}

func outboundImageExtension(mimeType string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	default:
		return "", false
	}
}

const outgoingThumbnailMaxDimension = 100

// outgoingImageThumbnail produces a small inline JPEG thumbnail for an outgoing
// image, matching official-client behaviour. It returns nil when the image
// can't be decoded. A simple box-average downscale is used to avoid pulling in
// a resize dependency; thumbnails are tiny so quality is sufficient.
func outgoingImageThumbnail(data []byte) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil
	}

	dstW, dstH := thumbnailDimensions(srcW, srcH, outgoingThumbnailMaxDimension)
	thumb := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for dy := 0; dy < dstH; dy++ {
		y0 := bounds.Min.Y + dy*srcH/dstH
		y1 := bounds.Min.Y + (dy+1)*srcH/dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := bounds.Min.X + dx*srcW/dstW
			x1 := bounds.Min.X + (dx+1)*srcW/dstW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rSum, gSum, bSum, count uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, b, _ := src.At(x, y).RGBA()
					rSum += uint64(r)
					gSum += uint64(g)
					bSum += uint64(b)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			thumb.SetRGBA(dx, dy, color.RGBA{
				R: uint8((rSum / count) >> 8),
				G: uint8((gSum / count) >> 8),
				B: uint8((bSum / count) >> 8),
				A: 0xFF,
			})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 70}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// thumbnailDimensions scales (srcW, srcH) so the longest side is at most max,
// preserving aspect ratio. Images already within bounds are returned unchanged.
func thumbnailDimensions(srcW, srcH, max int) (int, int) {
	if srcW <= max && srcH <= max {
		return srcW, srcH
	}
	if srcW >= srcH {
		h := srcH * max / srcW
		if h < 1 {
			h = 1
		}
		return max, h
	}
	w := srcW * max / srcH
	if w < 1 {
		w = 1
	}
	return w, max
}

func safeFilenamePart(input string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "@", "_")
	return replacer.Replace(input)
}

func (c *Client) signalSendQueue() {
	select {
	case c.sendQueueWake <- struct{}{}:
	default:
	}
}

func (c *Client) runSendQueue(ctx context.Context) {
	c.signalSendQueue()

	retry := time.NewTimer(0)
	if !retry.Stop() {
		<-retry.C
	}
	defer retry.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.sendQueueWake:
		}

		for {
			c.sendQueueMu.Lock()
			processed, err := c.drainSendQueue(ctx)
			c.sendQueueMu.Unlock()
			if err != nil {
				retry.Reset(5 * time.Second)
				select {
				case <-ctx.Done():
					return
				case <-c.sendQueueWake:
					if !retry.Stop() {
						<-retry.C
					}
				case <-retry.C:
				}
				continue
			}
			if !processed {
				break
			}
		}
	}
}

// sendQueueBackoff returns how long to wait before retrying a message
// that has failed attempt times.
func sendQueueBackoff(attempt int32) time.Duration {
	const base = 10 * time.Second
	const max = 5 * time.Minute
	if attempt <= 0 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 5 {
		shift = 5
	}
	delay := base * (1 << uint(shift))
	if delay > max {
		delay = max
	}
	return delay
}

func (c *Client) drainSendQueue(ctx context.Context) (bool, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return false, fmt.Errorf("WhatsApp client is not online")
	}

	pending, err := c.store.ListPendingOutgoingMessages(ctx, 25, time.Now())
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, nil
	}

	for _, message := range pending {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		if err := c.sendPendingMessage(ctx, client, message); err != nil {
			// Drop the timing entry; the retry attempt re-times from pickup.
			c.finishSendTiming(message.ID)
			return true, err
		}
	}

	return true, nil
}

func (c *Client) sendPendingMessage(ctx context.Context, client *whatsmeow.Client, message appstore.Message) error {
	c.markSendTiming(message.ID, func(t *sendTiming) { t.queuePickup = time.Now() })
	targetJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		c.markPendingMessageFailed(ctx, message.ID, "invalid chat ID")
		return nil
	}

	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))
	if message.MediaMimeType != "" || message.MediaLocalPath != "" {
		return c.sendPendingMediaMessage(ctx, client, targetJID, externalID, message)
	}

	outgoingMessage := &waE2E.Message{Conversation: proto.String(message.Text)}
	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		outgoingMessage = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(message.Text),
				ContextInfo: contextInfo,
			},
		}
	}

	if _, err := client.SendMessage(ctx, targetJID, outgoingMessage, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		// Transient: back off and retry.
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send message %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return fmt.Errorf("send text %s: %w", message.ID, err)
	}

	c.markPendingMessageSent(ctx, message.ID)
	return nil
}

func (c *Client) sendPendingMediaMessage(ctx context.Context, client *whatsmeow.Client, targetJID types.JID, externalID types.MessageID, message appstore.Message) error {
	if message.MediaKind == appstore.MediaKindSticker {
		return c.sendPendingStickerMessage(ctx, client, targetJID, externalID, message)
	}

	data, err := os.ReadFile(message.MediaLocalPath)
	if err != nil {
		// Forwarded copies of an undownloaded image carry only the original
		// sender's media payload; resend those keys instead of re-uploading.
		if sent, payloadErr := c.sendPendingMediaFromPayload(ctx, client, targetJID, externalID, message); sent || payloadErr != nil {
			return payloadErr
		}
		c.markPendingMessageFailed(ctx, message.ID, "cached media file missing or unreadable")
		return nil
	}

	mimeType := message.MediaMimeType
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	resp, err := client.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("upload media %s: %w", message.ID, err)
	}

	imgMsg := &waE2E.ImageMessage{
		Caption:       proto.String(message.Text),
		Mimetype:      proto.String(mimeType),
		URL:           &resp.URL,
		DirectPath:    &resp.DirectPath,
		MediaKey:      resp.MediaKey,
		FileEncSHA256: resp.FileEncSHA256,
		FileSHA256:    resp.FileSHA256,
		FileLength:    &resp.FileLength,
	}
	// Embed a small inline thumbnail like official clients do, so recipients
	// have a preview while the full image downloads and so quotes of this
	// image (here or on the other side) render a thumbnail.
	if thumb := outgoingImageThumbnail(data); len(thumb) > 0 {
		imgMsg.JPEGThumbnail = thumb
	}
	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		imgMsg.ContextInfo = contextInfo
	}

	if _, err := client.SendMessage(ctx, targetJID, &waE2E.Message{ImageMessage: imgMsg}, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send media %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return fmt.Errorf("send media %s: %w", message.ID, err)
	}

	// Persist the sent image proto (including thumbnail/media keys) so a later
	// reply quoting our own image can be reconstructed losslessly.
	if payload, marshalErr := proto.Marshal(imgMsg); marshalErr == nil {
		if _, dbErr := c.store.UpdateMessageMediaPayload(ctx, message.ID, payload); dbErr != nil {
			c.log.Warnf("Failed to persist sent image payload for %s: %v", message.ID, dbErr)
		}
	}

	c.markPendingMessageSent(ctx, message.ID)
	return nil
}

// sendPendingStickerMessage sends a queued sticker. The first send of a
// sticker uploads its cached file once and persists the resulting media keys
// (stickers.upload_payload); every later send of the same sticker skips the
// upload entirely. A send failure on reused keys invalidates the cache and
// retries with a fresh upload via the normal backoff.
func (c *Client) sendPendingStickerMessage(ctx context.Context, client *whatsmeow.Client, targetJID types.JID, externalID types.MessageID, message appstore.Message) error {
	sticker, ok, err := c.store.GetSticker(ctx, message.MediaCacheKey)
	if err != nil {
		return fmt.Errorf("load sticker %s: %w", message.MediaCacheKey, err)
	}
	if !ok {
		// Forwarded stickers usually aren't in the library; resend the
		// original media keys carried in the copied payload.
		if sent, payloadErr := c.sendPendingMediaFromPayload(ctx, client, targetJID, externalID, message); sent || payloadErr != nil {
			return payloadErr
		}
		c.markPendingMessageFailed(ctx, message.ID, "sticker is no longer in the library")
		return nil
	}

	var stickerMsg *waE2E.StickerMessage
	reusedUpload := false
	if len(sticker.UploadPayload) > 0 && time.Since(time.Unix(sticker.UploadTS, 0)) < stickerUploadReuseTTL {
		var cached waE2E.StickerMessage
		if proto.Unmarshal(sticker.UploadPayload, &cached) == nil && cached.GetDirectPath() != "" {
			stickerMsg = &cached
			reusedUpload = true
		}
	}
	if stickerMsg == nil {
		stickerMsg, err = c.uploadStickerForSend(ctx, client, sticker)
		if err != nil {
			newAttempts := message.SendAttempts + 1
			delay := sendQueueBackoff(newAttempts)
			c.log.Warnf("Failed to upload sticker %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
			if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
				c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
			}
			return fmt.Errorf("upload sticker %s: %w", message.ID, err)
		}
		if payload, marshalErr := proto.Marshal(stickerMsg); marshalErr == nil {
			if dbErr := c.store.SetStickerUploadPayload(ctx, sticker.CacheKey, payload, time.Now()); dbErr != nil {
				c.log.Warnf("Failed to cache sticker upload for %s: %v", sticker.CacheKey, dbErr)
			}
		}
	}

	outgoing := proto.Clone(stickerMsg).(*waE2E.StickerMessage)
	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		outgoing.ContextInfo = contextInfo
	}

	if _, err := client.SendMessage(ctx, targetJID, &waE2E.Message{StickerMessage: outgoing}, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		if reusedUpload {
			// The cached upload may have expired server-side; force a fresh
			// upload on the next attempt.
			if dbErr := c.store.SetStickerUploadPayload(ctx, sticker.CacheKey, nil, time.Now()); dbErr != nil {
				c.log.Warnf("Failed to invalidate sticker upload for %s: %v", sticker.CacheKey, dbErr)
			}
		}
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send sticker %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return fmt.Errorf("send sticker %s: %w", message.ID, err)
	}

	// Persist the sent proto (our upload's media keys, no context info) so a
	// later reply quoting this sticker reconstructs losslessly.
	if payload, marshalErr := proto.Marshal(stickerMsg); marshalErr == nil {
		if _, dbErr := c.store.UpdateMessageMediaPayload(ctx, message.ID, payload); dbErr != nil {
			c.log.Warnf("Failed to persist sent sticker payload for %s: %v", message.ID, dbErr)
		}
	}

	c.markPendingMessageSent(ctx, message.ID)
	return nil
}

// outgoingContextInfo combines the reply quote (if any) with WhatsApp's
// forwarded marker for messages queued by ForwardMessage.
func (c *Client) outgoingContextInfo(ctx context.Context, client *whatsmeow.Client, message appstore.Message) *waE2E.ContextInfo {
	contextInfo := c.outgoingReplyContextInfo(ctx, client, message)
	if message.IsForwarded {
		if contextInfo == nil {
			contextInfo = &waE2E.ContextInfo{}
		}
		contextInfo.IsForwarded = proto.Bool(true)
		contextInfo.ForwardingScore = proto.Uint32(1)
	}
	if mentionedJIDs := appstore.MentionJIDs(message.Mentions); len(mentionedJIDs) > 0 {
		if contextInfo == nil {
			contextInfo = &waE2E.ContextInfo{}
		}
		contextInfo.MentionedJID = mentionedJIDs
	}
	return contextInfo
}

// sendPendingMediaFromPayload sends a media message straight from its stored
// WhatsApp proto (original media keys, no upload). Returns sent=false when the
// payload is missing or unusable so the caller can fall through.
func (c *Client) sendPendingMediaFromPayload(ctx context.Context, client *whatsmeow.Client, targetJID types.JID, externalID types.MessageID, message appstore.Message) (bool, error) {
	if len(message.MediaPayload) == 0 {
		return false, nil
	}

	var outgoing *waE2E.Message
	switch message.MediaKind {
	case appstore.MediaKindSticker:
		sticker := &waE2E.StickerMessage{}
		if proto.Unmarshal(message.MediaPayload, sticker) != nil || sticker.GetDirectPath() == "" {
			return false, nil
		}
		clone := proto.Clone(sticker).(*waE2E.StickerMessage)
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		outgoing = &waE2E.Message{StickerMessage: clone}
	default:
		img := &waE2E.ImageMessage{}
		if proto.Unmarshal(message.MediaPayload, img) != nil || img.GetDirectPath() == "" {
			return false, nil
		}
		clone := proto.Clone(img).(*waE2E.ImageMessage)
		if message.Text != "" {
			clone.Caption = proto.String(message.Text)
		}
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		outgoing = &waE2E.Message{ImageMessage: clone}
	}

	if _, err := client.SendMessage(ctx, targetJID, outgoing, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send media payload %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return true, fmt.Errorf("send media payload %s: %w", message.ID, err)
	}

	c.markPendingMessageSent(ctx, message.ID)
	return true, nil
}

func (c *Client) outgoingReplyContextInfo(ctx context.Context, client *whatsmeow.Client, message appstore.Message) *waE2E.ContextInfo {
	if message.ReplyTo.MessageID == "" {
		return nil
	}

	// Prefer the original stored message so the quote carries the full media
	// proto (thumbnail, media keys, URL) recipients render the preview from.
	// Fall back to the thin reply summary only if the original is gone.
	stanzaSource := message.ReplyTo.MessageID
	senderID := message.ReplyTo.SenderID
	var quoted *waE2E.Message
	if original, err := c.store.GetMessage(ctx, message.ReplyTo.MessageID); err == nil {
		quoted = quotedMessageFromStored(original)
		stanzaSource = original.ID
		senderID = original.SenderID
	} else {
		quoted = quotedMessageForReply(message.ReplyTo)
	}

	contextInfo := &waE2E.ContextInfo{
		// RemoteJID is intentionally omitted for same-chat replies; setting it
		// can break jump-to on official clients.
		StanzaID:      proto.String(appstore.ExternalMessageID(message.ChatID, stanzaSource)),
		QuotedMessage: quoted,
	}
	if participant := c.outgoingReplyParticipant(ctx, client, message.ChatID, senderID); participant != "" {
		contextInfo.Participant = proto.String(participant)
	}
	return contextInfo
}

func (c *Client) outgoingReplyParticipant(ctx context.Context, client *whatsmeow.Client, chatID, senderID string) string {
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return ""
	}
	if senderID == "me" {
		if client == nil || client.Store.ID == nil {
			return ""
		}
		return client.Store.ID.ToNonAD().String()
	}
	jid, err := types.ParseJID(senderID)
	if err != nil {
		return senderID
	}
	// DMs are sent PN-addressed, so a LID participant won't match on the
	// recipient (breaks name resolution and jump-to). Normalize to the chat's
	// addressing mode; groups keep the stored member JID.
	if chatJID, err := types.ParseJID(chatID); err == nil && chatJID.Server != types.GroupServer && chatJID.Server != types.BroadcastServer {
		jid = c.normalizeJIDForChat(ctx, jid)
	}
	return jid.String()
}

// buildEditContent assembles the replacement message body for an edit. For a
// text message it mirrors the normal send path (Conversation, or
// ExtendedTextMessage when a reply quote/forward context must be preserved).
// For a media message only the caption is editable, so it reuses the persisted
// media proto (keys/URL/thumbnail) and swaps the caption rather than
// re-uploading. It returns nil for content that cannot be edited (e.g. a
// sticker, which has no caption, or media whose original proto is missing).
func (c *Client) buildEditContent(ctx context.Context, client *whatsmeow.Client, message appstore.Message, newText string) *waE2E.Message {
	if message.MediaMimeType != "" || message.MediaLocalPath != "" {
		if message.MediaKind != appstore.MediaKindImage {
			return nil
		}
		img := &waE2E.ImageMessage{}
		if len(message.MediaPayload) == 0 || proto.Unmarshal(message.MediaPayload, img) != nil || img.GetDirectPath() == "" {
			return nil
		}
		clone := proto.Clone(img).(*waE2E.ImageMessage)
		clone.Caption = proto.String(newText)
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		return &waE2E.Message{ImageMessage: clone}
	}

	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		return &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(newText),
				ContextInfo: contextInfo,
			},
		}
	}
	return &waE2E.Message{Conversation: proto.String(newText)}
}

// quotedMessageFromStored rebuilds the WhatsApp message proto to embed as the
// quoted message in a reply, reusing the full media sub-proto persisted in
// media_payload so stickers and photos render their thumbnails on the
// recipient's side.
func quotedMessageFromStored(message appstore.Message) *waE2E.Message {
	switch message.MediaKind {
	case appstore.MediaKindSticker:
		sticker := &waE2E.StickerMessage{}
		if len(message.MediaPayload) > 0 {
			if err := proto.Unmarshal(message.MediaPayload, sticker); err != nil {
				sticker = &waE2E.StickerMessage{}
			}
		}
		if sticker.Mimetype == nil && message.MediaMimeType != "" {
			sticker.Mimetype = proto.String(message.MediaMimeType)
		}
		return &waE2E.Message{StickerMessage: sticker}
	case appstore.MediaKindImage:
		img := &waE2E.ImageMessage{}
		if len(message.MediaPayload) > 0 {
			if err := proto.Unmarshal(message.MediaPayload, img); err != nil {
				img = &waE2E.ImageMessage{}
			}
		}
		if img.Mimetype == nil && message.MediaMimeType != "" {
			img.Mimetype = proto.String(message.MediaMimeType)
		}
		if img.Caption == nil && message.Text != "" {
			img.Caption = proto.String(message.Text)
		}
		return &waE2E.Message{ImageMessage: img}
	default:
		if message.Text != "" {
			return &waE2E.Message{Conversation: proto.String(message.Text)}
		}
		return &waE2E.Message{Conversation: proto.String(replyMediaSummary(message.MediaKind, message.MediaMimeType))}
	}
}

func quotedMessageForReply(reply appstore.MessageReply) *waE2E.Message {
	if reply.MediaKind == appstore.MediaKindSticker {
		sticker := &waE2E.StickerMessage{}
		if reply.MediaMimeType != "" {
			sticker.Mimetype = proto.String(reply.MediaMimeType)
		}
		return &waE2E.Message{StickerMessage: sticker}
	}
	if reply.MediaKind == appstore.MediaKindImage || strings.HasPrefix(reply.MediaMimeType, "image/") {
		image := &waE2E.ImageMessage{}
		if reply.Text != "" {
			image.Caption = proto.String(reply.Text)
		}
		if reply.MediaMimeType != "" {
			image.Mimetype = proto.String(reply.MediaMimeType)
		}
		return &waE2E.Message{ImageMessage: image}
	}
	// The quoted stub only has to be the right shape for the recipient's
	// renderer; the bytes themselves are never fetched from a quote.
	switch reply.MediaKind {
	case appstore.MediaKindVideo, appstore.MediaKindGIF, appstore.MediaKindVideoNote:
		video := &waE2E.VideoMessage{}
		if reply.Text != "" {
			video.Caption = proto.String(reply.Text)
		}
		if reply.MediaMimeType != "" {
			video.Mimetype = proto.String(reply.MediaMimeType)
		}
		if reply.MediaKind == appstore.MediaKindGIF {
			video.GifPlayback = proto.Bool(true)
		}
		if reply.MediaKind == appstore.MediaKindVideoNote {
			return &waE2E.Message{PtvMessage: video}
		}
		return &waE2E.Message{VideoMessage: video}
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		audio := &waE2E.AudioMessage{}
		if reply.MediaMimeType != "" {
			audio.Mimetype = proto.String(reply.MediaMimeType)
		}
		if reply.MediaKind == appstore.MediaKindVoice {
			audio.PTT = proto.Bool(true)
		}
		return &waE2E.Message{AudioMessage: audio}
	case appstore.MediaKindDocument:
		document := &waE2E.DocumentMessage{}
		if reply.Text != "" {
			document.FileName = proto.String(reply.Text)
		}
		if reply.MediaMimeType != "" {
			document.Mimetype = proto.String(reply.MediaMimeType)
		}
		return &waE2E.Message{DocumentMessage: document}
	}
	if reply.Text != "" {
		return &waE2E.Message{Conversation: proto.String(reply.Text)}
	}
	return &waE2E.Message{Conversation: proto.String(replyMediaSummary(reply.MediaKind, reply.MediaMimeType))}
}

func replyMediaSummary(mediaKind, mediaMimeType string) string {
	switch mediaKind {
	case appstore.MediaKindSticker:
		return "[Sticker]"
	case appstore.MediaKindVideo:
		return "[Video]"
	case appstore.MediaKindGIF:
		return "[GIF]"
	case appstore.MediaKindVideoNote:
		return "[Video message]"
	case appstore.MediaKindVoice:
		return "[Voice message]"
	case appstore.MediaKindAudio:
		return "[Audio]"
	case appstore.MediaKindDocument:
		return "[Document]"
	}
	switch {
	case mediaKind == appstore.MediaKindImage || strings.HasPrefix(mediaMimeType, "image/"):
		return "[Image]"
	case strings.HasPrefix(mediaMimeType, "video/"):
		return "[Video]"
	case strings.HasPrefix(mediaMimeType, "audio/"):
		return "[Audio]"
	case mediaKind != "" || mediaMimeType != "":
		return "[Media]"
	default:
		return "[Message]"
	}
}

func (c *Client) markPendingMessageSent(ctx context.Context, messageID string) {
	c.markSendTiming(messageID, func(t *sendTiming) { t.ackReturn = time.Now() })
	message, changed, err := c.store.UpdateMessageStatus(ctx, messageID, appstore.StatusSent)
	if err != nil {
		c.finishSendTiming(messageID)
		c.log.Warnf("Failed to mark queued message %s sent: %v", messageID, err)
		return
	}
	c.markSendTiming(messageID, func(t *sendTiming) { t.statusWrite = time.Now() })
	if changed {
		c.publishMessageStatusUpdated(ctx, message)
	}
	c.logSendTimeline(messageID, c.finishSendTiming(messageID))
}

func (c *Client) markPendingMessageFailed(ctx context.Context, messageID string, reason string) {
	c.finishSendTiming(messageID)
	message, changed, err := c.store.UpdateMessageStatus(ctx, messageID, appstore.StatusFailed)
	if err != nil {
		c.log.Warnf("Failed to mark queued message %s failed: %v", messageID, err)
		return
	}
	c.log.Warnf("Queued message %s permanently failed: %s", messageID, reason)
	if changed {
		c.publishMessageStatusUpdated(ctx, message)
	}
}

func (c *Client) handleReceipt(evt *events.Receipt, offlineSync bool) {
	status, ok := receiptStatus(evt.Type)
	if !ok {
		return
	}

	ctx := context.Background()
	normalizedChat := c.normalizeJIDForChat(ctx, evt.Chat)
	chatID := normalizedChat.String()
	if chatID == "" {
		return
	}

	isGroup := normalizedChat.Server == types.GroupServer || normalizedChat.Server == types.BroadcastServer
	kind, isParticipantReceipt := participantReceiptKind(evt.Type)
	clearUnread := receiptClearsLocalUnread(evt)

	participant := ""
	if isParticipantReceipt && !clearUnread {
		own := c.ownParticipantJIDs()
		canonical := c.canonicalParticipantJID(ctx, evt.Sender)
		isSelf := canonical == "" || own[canonical]
		switch {
		case !isGroup && isSelf:
			// 1:1 receipts may arrive without a usable sender; they can only
			// come from the peer.
			participant = c.canonicalParticipantJID(ctx, normalizedChat)
		case isSelf:
			// A non-read group receipt from our own device says nothing about other
			// members; treat it as a plain status update.
			isParticipantReceipt = false
		default:
			participant = canonical
		}
		if participant == "" {
			isParticipantReceipt = false
		}
	}

	if !clearUnread {
		for _, messageID := range evt.MessageIDs {
			internalID := internalMessageIDForChat(chatID, messageID)

			var message appstore.Message
			var changed bool
			var err error
			if isParticipantReceipt {
				message, changed, err = c.applyParticipantReceipt(ctx, normalizedChat, internalID, participant, kind, evt.Timestamp, status, isGroup, offlineSync)
			} else {
				message, changed, err = c.store.UpdateMessageStatus(ctx, internalID, status)
			}
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				c.log.Errorf("Failed to update message status for %s: %v", internalID, err)
				continue
			}
			if !changed {
				continue
			}

			if !offlineSync {
				c.publishMessageStatusUpdated(ctx, message)
			}
		}
	}

	// A self receipt means we read these messages on another device (the
	// phone): clear them from the chat's unread badge. Published even during
	// offline sync — one ChatUpdated per receipt keeps the badge honest.
	if clearUnread {
		internalIDs := make([]string, 0, len(evt.MessageIDs))
		for _, messageID := range evt.MessageIDs {
			internalIDs = append(internalIDs, internalMessageIDForChat(chatID, messageID))
		}
		chat, changed, err := c.store.MarkMessagesReadByIDs(ctx, chatID, internalIDs)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				c.log.Warnf("Failed to mark self-read messages in %s: %v", chatID, err)
			}
			return
		}
		if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
}

func receiptClearsLocalUnread(evt *events.Receipt) bool {
	switch evt.Type {
	case types.ReceiptTypeReadSelf, types.ReceiptTypePlayedSelf:
		return true
	case types.ReceiptTypeRead, types.ReceiptTypePlayed:
		return evt.IsFromMe
	default:
		return false
	}
}

// applyParticipantReceipt records one participant's receipt and derives the
// message's aggregate status. 1:1 chats keep the direct mapping (the peer is
// the only recipient); group messages advance to delivered/read only once
// every member has the receipt, mirroring WhatsApp's tick semantics.
func (c *Client) applyParticipantReceipt(ctx context.Context, chatJID types.JID, internalID, participant, kind string, ts time.Time, status string, isGroup, offlineSync bool) (appstore.Message, bool, error) {
	message, err := c.store.GetMessage(ctx, internalID)
	if err != nil {
		return appstore.Message{}, false, err
	}

	// Receipt rows only matter for our own messages (ticks + message info).
	if message.Direction != appstore.DirectionOutgoing {
		return c.store.UpdateMessageStatus(ctx, internalID, status)
	}

	if err := c.store.UpsertMessageReceipt(ctx, internalID, message.ChatID, participant, kind, ts); err != nil {
		c.log.Warnf("Failed to record receipt for %s from %s: %v", internalID, participant, err)
	} else if !offlineSync {
		// The per-member breakdown changed even when the aggregate status below
		// does not; the `receipts` view keys off this to re-derive live. Skipped
		// during offline sync, matching the status-publish gating in the caller.
		c.daemon.PublishMessageReceipt(message.ChatID, internalID)
	}

	if !isGroup {
		return c.store.UpdateMessageStatus(ctx, internalID, status)
	}

	participants, known := c.groupReceiptParticipants(ctx, chatJID)
	if !known {
		// Membership unknown: keep the any-member behavior rather than
		// freezing the ticks at "sent" forever.
		return c.store.UpdateMessageStatus(ctx, internalID, status)
	}

	aggregate, err := c.aggregateGroupStatus(ctx, internalID, participants)
	if err != nil {
		return appstore.Message{}, false, err
	}
	if aggregate == "" {
		return message, false, nil
	}
	return c.store.UpdateMessageStatus(ctx, internalID, aggregate)
}

// aggregateGroupStatus computes the WhatsApp group tick state: delivered when
// every member has received the message, read when every member has read it.
// Empty means neither threshold is met yet.
func (c *Client) aggregateGroupStatus(ctx context.Context, internalID string, participants []string) (string, error) {
	receipts, err := c.store.ListMessageReceipts(ctx, internalID)
	if err != nil {
		return "", err
	}
	byJID := make(map[string]appstore.MessageReceipt, len(receipts))
	for _, receipt := range receipts {
		byJID[receipt.ParticipantJID] = receipt
	}

	allDelivered, allRead := true, true
	for _, participant := range participants {
		receipt, ok := byJID[participant]
		if !ok || receipt.DeliveredTs == 0 {
			allDelivered = false
		}
		if !ok || receipt.ReadTs == 0 {
			allRead = false
		}
		if !allDelivered && !allRead {
			return "", nil
		}
	}

	switch {
	case allRead:
		return appstore.StatusRead, nil
	case allDelivered:
		return appstore.StatusDelivered, nil
	default:
		return "", nil
	}
}

// participantReceiptKind maps receipt types that represent another user's
// delivery/read state. Self receipts (our own other devices) and server
// receipts don't describe a recipient and report false.
func participantReceiptKind(receiptType types.ReceiptType) (string, bool) {
	switch receiptType {
	case types.ReceiptTypeDelivered:
		return appstore.ReceiptKindDelivered, true
	case types.ReceiptTypeRead:
		return appstore.ReceiptKindRead, true
	case types.ReceiptTypePlayed:
		return appstore.ReceiptKindPlayed, true
	default:
		return "", false
	}
}

func (c *Client) publishMessageStatusUpdated(ctx context.Context, message appstore.Message) {
	c.daemon.PublishMessageUpdated(toDaemonMessage(message))

	chat, err := c.store.GetChat(ctx, message.ChatID)
	if err != nil {
		c.log.Warnf("Failed to load chat after status update for %s: %v", message.ID, err)
		return
	}
	if chat.LastMessageTime != message.TimestampUnix {
		return
	}

	c.daemon.PublishChatUpdated(toDaemonChat(chat))
}

func (c *Client) MarkChatRead(ctx context.Context, chatID string) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	readCandidates, err := c.store.ReadCandidatesForChat(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}
	c.sendReadReceipts(ctx, chat, chatID, readCandidates)

	updatedChat, err := c.store.MarkMessagesRead(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}

	c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	return updatedChat, nil
}

func (c *Client) MarkChatReadUpTo(ctx context.Context, chatID, upToMessageID string) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	target, err := c.store.GetMessage(ctx, strings.TrimSpace(upToMessageID))
	if err != nil {
		return appstore.Chat{}, err
	}
	if target.ChatID != chatID {
		return appstore.Chat{}, sql.ErrNoRows
	}

	readCandidates, err := c.store.ReadCandidatesForChat(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}
	bounded := readCandidates[:0]
	internalIDs := make([]string, 0, len(readCandidates))
	for _, candidate := range readCandidates {
		if candidate.TimestampUnix < target.TimestampUnix || (candidate.TimestampUnix == target.TimestampUnix && candidate.SortSeq <= target.SortSeq) {
			bounded = append(bounded, candidate)
			internalIDs = append(internalIDs, candidate.InternalID)
		}
	}
	// Commit the local read state first, then emit upstream read receipts: a
	// failed store write must not happen after WhatsApp (and other devices) have
	// already been told the messages were read.
	updatedChat, changed, err := c.store.MarkMessagesReadByIDs(ctx, chatID, internalIDs)
	if err != nil {
		return appstore.Chat{}, err
	}
	c.sendReadReceipts(ctx, chat, chatID, bounded)
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

// MarkMessagePlayed reports that the user listened to an inbound voice note.
// WhatsApp models this as a read receipt with the "played" type, which is what
// turns the sender's mic icon blue. The store flag makes it idempotent: the
// receipt goes out once, however many times the bubble is replayed.
func (c *Client) MarkMessagePlayed(ctx context.Context, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "message_id is required")
	}

	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return err
	}
	if message.MediaKind != appstore.MediaKindVoice {
		return app.NewCommandError(app.CommandErrorRejected, "only voice messages can be marked played")
	}
	// Our own voice notes are played receipts we would be sending to ourselves.
	if message.Direction == appstore.DirectionOutgoing {
		return nil
	}

	updated, flipped, err := c.store.MarkMessageMediaPlayed(ctx, messageID)
	if err != nil {
		return err
	}
	if !flipped {
		return nil
	}
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))

	info, err := mediaRetryMessageInfo(updated)
	if err != nil {
		return err
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		// The local flag stands; the receipt is best-effort, exactly as an
		// ordinary read receipt is when offline.
		return nil
	}
	sender := info.Sender
	if !info.IsGroup {
		sender = types.EmptyJID
	}
	if err := client.MarkRead(ctx, []types.MessageID{info.ID}, time.Now(), info.Chat, sender, types.ReceiptTypePlayed); err != nil {
		c.log.Warnf("Failed to send played receipt for %s: %v", messageID, err)
	}
	return nil
}

func (c *Client) sendReadReceipts(ctx context.Context, chat types.JID, chatID string, readCandidates []appstore.ReadCandidate) {
	if len(readCandidates) == 0 {
		return
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}
	for _, batch := range buildReadBatches(chat, readCandidates) {
		if len(batch.messageIDs) == 0 {
			continue
		}
		if err := client.MarkRead(ctx, batch.messageIDs, time.Now(), chat, batch.sender); err != nil {
			c.log.Warnf("Failed to send read receipt for %s: %v", chatID, err)
		}
	}
}

func (c *Client) SetChatPinned(ctx context.Context, chatID string, pinned bool) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}
	if pinned {
		pinnedCount, err := c.store.PinnedChatCountExcluding(ctx, chatID)
		if err != nil {
			return appstore.Chat{}, err
		}
		if pinnedCount >= maxPinnedChats {
			return appstore.Chat{}, app.NewCommandError(app.CommandErrorRejected, "You can only pin %d chats", maxPinnedChats)
		}
	}
	if err := c.sendRegularLowAppState(ctx, client, appstate.BuildPin(chat, pinned)); err != nil {
		return appstore.Chat{}, err
	}

	order := uint32(0)
	if pinned {
		order = uint32(time.Now().Unix())
	}
	updatedChat, changed, err := c.store.UpdateChatPinState(ctx, chatID, pinned, order)
	if err != nil {
		return appstore.Chat{}, err
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

func (c *Client) SetChatArchived(ctx context.Context, chatID string, archived bool) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}

	// Last-message timestamp/key are optional for BuildArchive; zero values are
	// accepted. WhatsApp auto-unpins an archived chat, so we mirror that locally.
	if err := c.sendRegularLowAppState(ctx, client, appstate.BuildArchive(chat, archived, time.Time{}, nil)); err != nil {
		return appstore.Chat{}, err
	}

	if archived {
		if unpinned, changed, err := c.store.UpdateChatPinState(ctx, chatID, false, 0); err != nil {
			return appstore.Chat{}, err
		} else if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(unpinned))
		}
	}

	updatedChat, changed, err := c.store.UpdateChatArchiveState(ctx, chatID, archived)
	if err != nil {
		return appstore.Chat{}, err
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

// SetChatMuted mutes or unmutes a chat and syncs it to the device. A zero
// duration with muted=true means "forever" (stored as -1); otherwise the chat
// stays muted until now+duration. Muting uses the regular_high app-state
// collection, mirroring message starring.
func (c *Client) SetChatMuted(ctx context.Context, chatID string, muted bool, duration time.Duration) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}

	if err := c.sendRegularHighAppState(ctx, client, appstate.BuildMute(chat, muted, duration)); err != nil {
		return appstore.Chat{}, err
	}

	var muteEnd int64
	if muted {
		if duration > 0 {
			muteEnd = time.Now().Add(duration).UnixMilli()
		} else {
			muteEnd = -1
		}
	}

	updatedChat, changed, err := c.store.UpdateChatMuteState(ctx, chatID, muted, muteEnd)
	if err != nil {
		return appstore.Chat{}, err
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

func (c *Client) sendRegularLowAppState(ctx context.Context, client *whatsmeow.Client, patch appstate.PatchInfo) error {
	c.appStateMu.Lock()
	defer c.appStateMu.Unlock()

	if err := client.SendAppState(ctx, patch); err != nil {
		if !isAppStateConflictError(err) {
			return err
		}
		c.log.Warnf("WhatsApp app state conflict while updating pins; resyncing regular_low and retrying: %v", err)
		if _, syncErr := fetchFullRegularLowAppState(ctx, client); syncErr != nil {
			return app.NewCommandError(app.CommandErrorRejected, "WhatsApp sync conflict. Try again in a moment.")
		}
		if retryErr := client.SendAppState(ctx, patch); retryErr != nil {
			return app.NewCommandError(app.CommandErrorRejected, "WhatsApp sync conflict. Try again in a moment.")
		}
	}
	return nil
}

func isAppStateConflictError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, appstate.ErrMismatchingLTHash) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, `code="409"`) ||
		strings.Contains(message, "mismatching LTHash") ||
		strings.Contains(message, "failed to verify patch")
}

func receiptStatus(receiptType types.ReceiptType) (string, bool) {
	switch receiptType {
	case types.ReceiptTypeDelivered:
		return appstore.StatusDelivered, true
	case types.ReceiptTypeSender:
		return appstore.StatusSent, true
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf, types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return appstore.StatusRead, true
	case types.ReceiptTypeServerError:
		return appstore.StatusFailed, true
	default:
		return "", false
	}
}

func buildReadBatches(chat types.JID, candidates []appstore.ReadCandidate) []readBatch {
	grouped := make(map[string]*readBatch)
	order := make([]string, 0)

	for _, candidate := range candidates {
		sender, err := senderForReadReceipt(chat, candidate.SenderID)
		if err != nil {
			continue
		}

		key := sender.String()
		batch, ok := grouped[key]
		if !ok {
			batch = &readBatch{sender: sender, messageIDs: make([]types.MessageID, 0, 8)}
			grouped[key] = batch
			order = append(order, key)
		}

		batch.messageIDs = append(batch.messageIDs, types.MessageID(candidate.ExternalID))
	}

	batches := make([]readBatch, 0, len(order))
	for _, key := range order {
		batches = append(batches, *grouped[key])
	}

	return batches
}

func senderForReadReceipt(chat types.JID, senderID string) (types.JID, error) {
	if chat.Server == types.GroupServer || chat.Server == types.BroadcastServer {
		return types.ParseJID(senderID)
	}

	if senderID != "" && senderID != "me" {
		return types.ParseJID(senderID)
	}

	return chat, nil
}

func internalMessageIDForChat(chatID string, messageID types.MessageID) string {
	return fmt.Sprintf("%s:%s", chatID, messageID)
}
