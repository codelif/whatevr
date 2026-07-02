package wa

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// GetMessageInfo assembles the receipt timeline for one message: sent time,
// aggregate delivered/read times, and for groups the per-member breakdown
// (including members with no receipt yet).
func (c *Client) GetMessageInfo(ctx context.Context, messageID string) (app.MessageInfo, error) {
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return app.MessageInfo{}, err
	}
	chat, err := c.store.GetChat(ctx, message.ChatID)
	if err != nil {
		return app.MessageInfo{}, err
	}
	receipts, err := c.store.ListMessageReceipts(ctx, messageID)
	if err != nil {
		return app.MessageInfo{}, err
	}

	info := app.MessageInfo{
		Status:     message.Status,
		SentTsUnix: message.TimestampUnix,
		IsGroup:    chat.IsGroup,
	}

	if !chat.IsGroup {
		if len(receipts) > 0 {
			info.DeliveredTsUnix = receipts[0].DeliveredTs
			info.ReadTsUnix = receipts[0].ReadTs
		}
		return info, nil
	}

	byJID := make(map[string]appstore.MessageReceipt, len(receipts))
	for _, receipt := range receipts {
		byJID[receipt.ParticipantJID] = receipt
	}

	memberJIDs := make([]string, 0, len(receipts)+8)
	seen := make(map[string]bool, len(receipts)+8)
	if chatJID, err := types.ParseJID(message.ChatID); err == nil {
		if participants, known := c.groupReceiptParticipants(ctx, chatJID); known {
			for _, jid := range participants {
				if !seen[jid] {
					seen[jid] = true
					memberJIDs = append(memberJIDs, jid)
				}
			}
		}
	}
	// Receipt rows from members who since left still describe what happened.
	for _, receipt := range receipts {
		if !seen[receipt.ParticipantJID] {
			seen[receipt.ParticipantJID] = true
			memberJIDs = append(memberJIDs, receipt.ParticipantJID)
		}
	}

	allDelivered, allRead := len(memberJIDs) > 0, len(memberJIDs) > 0
	var lastDelivered, lastRead int64
	for _, jid := range memberJIDs {
		receipt := byJID[jid]
		entry := app.ParticipantReceipt{
			JID:             jid,
			DeliveredTsUnix: receipt.DeliveredTs,
			ReadTsUnix:      receipt.ReadTs,
			PlayedTsUnix:    receipt.PlayedTs,
		}
		entry.DisplayName, entry.AvatarLocalPath = c.participantDisplay(ctx, jid)
		info.Receipts = append(info.Receipts, entry)

		if receipt.DeliveredTs == 0 {
			allDelivered = false
		} else if receipt.DeliveredTs > lastDelivered {
			lastDelivered = receipt.DeliveredTs
		}
		if receipt.ReadTs == 0 {
			allRead = false
		} else if receipt.ReadTs > lastRead {
			lastRead = receipt.ReadTs
		}
	}
	if allDelivered {
		info.DeliveredTsUnix = lastDelivered
	}
	if allRead {
		info.ReadTsUnix = lastRead
	}
	return info, nil
}

func (c *Client) participantDisplay(ctx context.Context, jid string) (string, string) {
	ids := c.participantDisplayIDs(ctx, jid)
	fallbackName := ""
	for _, id := range ids {
		name, avatar, err := c.store.SenderDisplay(ctx, id)
		if err != nil {
			c.log.Warnf("Failed to load sender display for %s: %v", id, err)
			continue
		}
		if fallbackName == "" && name != "" {
			fallbackName = name
		}
		if avatar != "" {
			if name == "" {
				name = fallbackName
			}
			if name == "" {
				name = c.senderNameForParticipantID(ctx, id)
			}
			return name, avatar
		}
	}

	for _, id := range ids {
		c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectSender, ID: id}, avatarPriorityVisible)
	}

	if fallbackName == "" {
		for _, id := range ids {
			if name := c.senderNameForParticipantID(ctx, id); name != "" {
				fallbackName = name
				break
			}
		}
	}
	return fallbackName, ""
}

func (c *Client) participantDisplayIDs(ctx context.Context, jid string) []string {
	seen := make(map[string]bool, 4)
	ids := make([]string, 0, 4)
	addID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	addJID := func(jid types.JID) {
		if jid.IsEmpty() {
			return
		}
		addID(bareAvatarJID(jid).String())
	}

	trimmed := strings.TrimSpace(jid)
	addID(trimmed)
	parsed, err := types.ParseJID(trimmed)
	if err != nil {
		return ids
	}

	parsed = bareAvatarJID(parsed)
	normalized := bareAvatarJID(c.normalizeJIDForChat(ctx, parsed))
	addJID(parsed)
	addJID(normalized)

	client := c.currentClient()
	if client == nil || client.Store == nil || client.Store.LIDs == nil {
		return ids
	}
	for _, candidate := range []types.JID{parsed, normalized} {
		switch candidate.Server {
		case types.DefaultUserServer:
			if lid, err := client.Store.LIDs.GetLIDForPN(ctx, candidate); err == nil {
				addJID(lid)
			}
		case types.HiddenUserServer:
			if pn, err := client.Store.LIDs.GetPNForLID(ctx, candidate); err == nil {
				addJID(pn)
			}
		}
	}
	return ids
}

func (c *Client) senderNameForParticipantID(ctx context.Context, id string) string {
	parsed, err := types.ParseJID(id)
	if err != nil {
		return ""
	}
	return c.senderName(ctx, parsed)
}

// DeleteMessageForMe removes a message from the local store only.
func (c *Client) DeleteMessageForMe(ctx context.Context, messageID string) error {
	message, chat, existed, err := c.store.DeleteMessageForMe(ctx, messageID)
	if err != nil {
		return err
	}
	if !existed {
		return sql.ErrNoRows
	}
	c.daemon.PublishMessageDeleted(message.ChatID, message.ID, toDaemonChat(chat))
	return nil
}

// RevokeMessage performs WhatsApp's "delete for everyone" on one of our own
// messages, then tombstones the local row. The server enforces the revoke
// window; its rejection is surfaced as the RPC error.
func (c *Client) RevokeMessage(ctx context.Context, messageID string) (appstore.Message, error) {
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return appstore.Message{}, err
	}
	if message.Direction != appstore.DirectionOutgoing {
		return appstore.Message{}, grpcstatus.Error(codes.FailedPrecondition, "only your own messages can be deleted for everyone")
	}
	if message.IsRevoked {
		return message, nil
	}

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Message{}, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not logged in")
	}

	chatJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		return appstore.Message{}, grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}

	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))
	if _, err := client.SendMessage(ctx, chatJID, client.BuildRevoke(chatJID, types.EmptyJID, externalID)); err != nil {
		return appstore.Message{}, grpcstatus.Errorf(codes.Unknown, "delete for everyone failed: %v", err)
	}

	updated, chat, changed, err := c.store.MarkMessageRevoked(ctx, message.ID)
	if err != nil {
		return appstore.Message{}, err
	}
	if changed {
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return updated, nil
}

// EditMessage edits one of our own sent messages in place: the body of a text
// message or the caption of a media message. WhatsApp only allows editing for
// a limited window after sending (whatsmeow.EditWindow); the server also
// enforces this and its rejection is surfaced as the RPC error.
func (c *Client) EditMessage(ctx context.Context, messageID, newText string) (appstore.Message, error) {
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return appstore.Message{}, err
	}
	if message.Direction != appstore.DirectionOutgoing {
		return appstore.Message{}, grpcstatus.Error(codes.FailedPrecondition, "only your own messages can be edited")
	}
	if message.IsRevoked {
		return appstore.Message{}, grpcstatus.Error(codes.FailedPrecondition, "a deleted message cannot be edited")
	}
	if time.Since(time.Unix(message.TimestampUnix, 0)) > whatsmeow.EditWindow {
		return appstore.Message{}, grpcstatus.Error(codes.FailedPrecondition, "the edit window for this message has expired")
	}

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Message{}, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not logged in")
	}

	chatJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		return appstore.Message{}, grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}

	content := c.buildEditContent(ctx, client, message, newText)
	if content == nil {
		return appstore.Message{}, grpcstatus.Error(codes.FailedPrecondition, "this message cannot be edited")
	}

	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))
	if _, err := client.SendMessage(ctx, chatJID, client.BuildEdit(chatJID, externalID, content)); err != nil {
		return appstore.Message{}, grpcstatus.Errorf(codes.Unknown, "edit failed: %v", err)
	}

	// nil preserves the existing mentions: editing our own message doesn't
	// re-author @-mentions.
	updated, chat, changed, err := c.store.UpdateMessageText(ctx, message.ID, newText, nil)
	if err != nil {
		return appstore.Message{}, err
	}
	if changed {
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return updated, nil
}

// ForwardMessage queues copies of a stored message to the target chats via
// the regular pending-send queue (so offline/retry behavior applies). Media
// copies reuse the source's cached file and payload; the send path marks them
// with WhatsApp's forwarded context.
func (c *Client) ForwardMessage(ctx context.Context, sourceMessageID string, targetChatIDs []string) ([]appstore.SavedTextMessage, error) {
	client := c.currentClient()
	if client == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return nil, grpcstatus.Error(codes.FailedPrecondition, "WhatsApp session is not logged in")
	}

	source, err := c.store.GetMessage(ctx, sourceMessageID)
	if err != nil {
		return nil, err
	}
	if source.IsRevoked {
		return nil, grpcstatus.Error(codes.FailedPrecondition, "deleted messages cannot be forwarded")
	}
	isMedia := source.MediaKind != "" || source.MediaMimeType != "" || source.MediaLocalPath != ""
	if !isMedia && strings.TrimSpace(source.Text) == "" {
		return nil, grpcstatus.Error(codes.FailedPrecondition, "message has no content to forward")
	}
	if isMedia && source.MediaLocalPath == "" && len(source.MediaPayload) == 0 {
		return nil, grpcstatus.Error(codes.FailedPrecondition, "download the media before forwarding it")
	}

	saved := make([]appstore.SavedTextMessage, 0, len(targetChatIDs))
	for _, targetChatID := range targetChatIDs {
		targetJID, err := types.ParseJID(strings.TrimSpace(targetChatID))
		if err != nil {
			return saved, grpcstatus.Errorf(codes.InvalidArgument, "invalid target chat_id %q: %v", targetChatID, err)
		}
		targetJID = c.normalizeJIDForChat(ctx, targetJID)
		chatID := targetJID.String()

		input := appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, client.GenerateMessageID()),
			ChatID:      chatID,
			SenderID:    "me",
			Text:        source.Text,
			Timestamp:   time.Now(),
			Direction:   appstore.DirectionOutgoing,
			Status:      appstore.StatusPending,
			IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
			CountUnread: false,
			IsForwarded: true,
		}

		var result appstore.SavedTextMessage
		if isMedia {
			result, err = c.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
				TextMessageInput:        input,
				MediaKind:               source.MediaKind,
				MediaMimeType:           source.MediaMimeType,
				MediaLocalPath:          source.MediaLocalPath,
				MediaThumbnailLocalPath: source.MediaThumbnailLocalPath,
				MediaWidth:              source.MediaWidth,
				MediaHeight:             source.MediaHeight,
				MediaAnimated:           source.MediaAnimated,
				MediaPayload:            source.MediaPayload,
				MediaCacheKey:           source.MediaCacheKey,
			})
		} else {
			result, err = c.store.SaveTextMessage(ctx, input)
		}
		if err != nil {
			return saved, err
		}

		if result.Inserted {
			c.log.Infof("Queued forwarded message %s to %s", result.Message.ID, chatID)
			c.daemon.PublishNewMessage(toDaemonMessage(result.Message), toDaemonChat(result.Chat))
		}
		c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID}, avatarPriorityVisible)
		saved = append(saved, result)
	}

	c.signalSendQueue()
	return saved, nil
}

// IsNotFound reports whether err is the store's missing-row error; the rpc
// layer maps it to NOT_FOUND.
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
