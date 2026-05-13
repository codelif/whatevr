package wa

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	appstore "whatevrd/internal/store"
)

const maxOutboundMediaBytes = 25 * 1024 * 1024

type readBatch struct {
	sender     types.JID
	messageIDs []types.MessageID
}

func (c *Client) SendText(ctx context.Context, chatID, text string) (appstore.SavedTextMessage, error) {
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.SavedTextMessage{}, grpcstatus.Error(codes.FailedPrecondition, "WhatsApp session is not logged in")
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return appstore.SavedTextMessage{}, grpcstatus.Error(codes.InvalidArgument, "text is required")
	}

	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()

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
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	if saved.Inserted {
		c.log.Infof("Queued text message %s to %s", saved.Message.ID, chatID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
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
		return grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
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
		return grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}
	if jid.Server == types.GroupServer {
		return nil
	}

	if err := client.SendPresence(ctx, types.PresenceAvailable); err != nil {
		return err
	}
	return client.SubscribePresence(ctx, jid)
}

func (c *Client) SendMedia(ctx context.Context, chatID, filePath, caption string) (appstore.SavedTextMessage, error) {
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.SavedTextMessage{}, grpcstatus.Error(codes.FailedPrecondition, "WhatsApp session is not logged in")
	}

	data, mimeType, extension, err := readOutboundMedia(filePath)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()

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
		},
		MediaMimeType:  mimeType,
		MediaLocalPath: localPath,
		MediaWidth:     mediaWidth,
		MediaHeight:    mediaHeight,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	if saved.Inserted {
		c.log.Infof("Queued media message %s to %s", saved.Message.ID, chatID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	c.signalSendQueue()
	return saved, nil
}

func readOutboundMedia(filePath string) ([]byte, string, string, error) {
	if !filepath.IsAbs(filePath) {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file path must be absolute")
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file is not accessible")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file must be a regular file")
	}
	if info.Size() <= 0 {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file is empty")
	}
	if info.Size() > maxOutboundMediaBytes {
		return nil, "", "", grpcstatus.Errorf(codes.InvalidArgument, "media file must be <= %d MiB", maxOutboundMediaBytes/(1024*1024))
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, "", "", grpcstatus.Error(codes.PermissionDenied, "media file owner is not allowed")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file is not readable")
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxOutboundMediaBytes+1))
	if err != nil {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file could not be read")
	}
	if len(data) > maxOutboundMediaBytes {
		return nil, "", "", grpcstatus.Errorf(codes.InvalidArgument, "media file must be <= %d MiB", maxOutboundMediaBytes/(1024*1024))
	}
	mimeType := http.DetectContentType(data)
	extension, ok := outboundImageExtension(mimeType)
	if !ok {
		return nil, "", "", grpcstatus.Error(codes.InvalidArgument, "media file must be a supported image")
	}
	return data, mimeType, extension, nil
}

func outboundImageExtension(mimeType string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	default:
		return "", false
	}
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
				select {
				case <-ctx.Done():
					return
				case <-c.sendQueueWake:
				case <-time.After(5 * time.Second):
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
			return true, err
		}
	}

	return true, nil
}

func (c *Client) sendPendingMessage(ctx context.Context, client *whatsmeow.Client, message appstore.Message) error {
	targetJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		c.markPendingMessageFailed(ctx, message.ID, "invalid chat ID")
		return nil
	}

	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))
	if message.MediaMimeType != "" || message.MediaLocalPath != "" {
		return c.sendPendingMediaMessage(ctx, client, targetJID, externalID, message)
	}

	if _, err := client.SendMessage(ctx, targetJID, &waE2E.Message{
		Conversation: proto.String(message.Text),
	}, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
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
	data, err := os.ReadFile(message.MediaLocalPath)
	if err != nil {
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

	if _, err := client.SendMessage(ctx, targetJID, &waE2E.Message{ImageMessage: imgMsg}, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send media %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return fmt.Errorf("send media %s: %w", message.ID, err)
	}

	c.markPendingMessageSent(ctx, message.ID)
	return nil
}

func (c *Client) markPendingMessageSent(ctx context.Context, messageID string) {
	message, changed, err := c.store.UpdateMessageStatus(ctx, messageID, appstore.StatusSent)
	if err != nil {
		c.log.Warnf("Failed to mark queued message %s sent: %v", messageID, err)
		return
	}
	if changed {
		c.publishMessageStatusUpdated(ctx, message)
	}
}

func (c *Client) markPendingMessageFailed(ctx context.Context, messageID string, reason string) {
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

func (c *Client) handleReceipt(evt *events.Receipt) {
	status, ok := receiptStatus(evt.Type)
	if !ok {
		return
	}

	normalizedChat := c.normalizeJIDForChat(context.Background(), evt.Chat)
	chatID := normalizedChat.String()
	if chatID == "" {
		return
	}

	for _, messageID := range evt.MessageIDs {
		internalID := internalMessageIDForChat(chatID, messageID)
		message, changed, err := c.store.UpdateMessageStatus(context.Background(), internalID, status)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			c.log.Errorf("Failed to update message status for %s: %v", internalID, err)
			continue
		}
		if !changed {
			continue
		}

		c.publishMessageStatusUpdated(context.Background(), message)
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
		return appstore.Chat{}, grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	readCandidates, err := c.store.ReadCandidatesForChat(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}

	if len(readCandidates) > 0 {
		client := c.currentClient()
		if client != nil && client.IsLoggedIn() {
			for _, batch := range buildReadBatches(chat, readCandidates) {
				if len(batch.messageIDs) == 0 {
					continue
				}
				if err := client.MarkRead(ctx, batch.messageIDs, time.Now(), chat, batch.sender); err != nil {
					c.log.Warnf("Failed to send read receipt for %s: %v", chatID, err)
				}
			}
		}
	}

	updatedChat, err := c.store.MarkMessagesRead(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}

	c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	return updatedChat, nil
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
