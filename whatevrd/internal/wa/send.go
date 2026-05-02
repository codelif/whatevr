package wa

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

type readBatch struct {
	sender     types.JID
	messageIDs []types.MessageID
}

func (c *Client) SendText(ctx context.Context, chatID, text string) (appstore.SavedTextMessage, error) {
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not initialized")
	}
	if !client.IsLoggedIn() {
		return appstore.SavedTextMessage{}, grpcstatus.Error(codes.FailedPrecondition, "whatevrd is not online")
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
	resp, err := client.SendMessage(ctx, targetJID, &waE2E.Message{
		Conversation: proto.String(trimmedText),
	}, whatsmeow.SendRequestExtra{ID: messageID})
	if err != nil {
		return appstore.SavedTextMessage{}, grpcstatus.Errorf(codes.Unavailable, "send failed: %v", err)
	}

	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	saved, err := c.store.SaveTextMessage(ctx, appstore.TextMessageInput{
		ID:          internalMessageIDForChat(chatID, messageID),
		ChatID:      chatID,
		ChatName:    "",
		SenderID:    ownSenderID(resp.Sender),
		Text:        trimmedText,
		Timestamp:   timestamp,
		Direction:   appstore.DirectionOutgoing,
		Status:      appstore.StatusSent,
		IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
		CountUnread: false,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	c.log.Infof("Sent text message %s to %s", saved.Message.ID, chatID)
	c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	return saved, nil
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

		c.daemon.PublishMessageUpdated(toDaemonMessage(message))
	}
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
	case types.ReceiptTypeDelivered, types.ReceiptTypeSender:
		return appstore.StatusDelivered, true
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf, types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return appstore.StatusRead, true
	case types.ReceiptTypeServerError:
		return appstore.StatusFailed, true
	default:
		return "", false
	}
}

func ownSenderID(sender types.JID) string {
	if !sender.IsEmpty() {
		return sender.String()
	}
	return "me"
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
