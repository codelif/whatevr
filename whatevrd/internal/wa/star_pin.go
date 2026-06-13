package wa

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	appstore "whatevrd/internal/store"
)

const (
	maxPinnedMessages = 3
	// Fallback pin lifetime when an incoming pin omits its duration (WhatsApp's
	// "7 days" default).
	defaultPinDurationSecs = 7 * 24 * 60 * 60
)

// SetMessageStarred stars or unstars a message, syncing the change to WhatsApp
// via a regular_high app-state mutation and mirroring it into the local store.
// Returns the reloaded target message.
func (c *Client) SetMessageStarred(ctx context.Context, messageID string, starred bool) (appstore.Message, error) {
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return appstore.Message{}, err
	}

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Message{}, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not logged in")
	}

	chatJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		return appstore.Message{}, grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}

	fromMe := message.Direction == appstore.DirectionOutgoing
	sender := messageSenderJID(client, message, chatJID, fromMe)
	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))

	patch := appstate.BuildStar(chatJID, sender, externalID, fromMe, starred)
	if err := c.sendRegularHighAppState(ctx, client, patch); err != nil {
		return appstore.Message{}, err
	}

	updated, changed, err := c.store.SetMessageStarred(ctx, message.ID, starred)
	if err != nil {
		return appstore.Message{}, err
	}
	if changed {
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	}
	return updated, nil
}

// PinMessage pins or unpins a message for everyone in its chat. Pinning sends a
// PinInChatMessage carrying the chosen lifetime; unpinning sends UNPIN_FOR_ALL.
// The local store is updated to reflect the new pin window. Returns the reloaded
// target message.
func (c *Client) PinMessage(ctx context.Context, messageID string, pinned bool, durationSecs uint32) (appstore.Message, error) {
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return appstore.Message{}, err
	}
	if message.IsRevoked {
		return appstore.Message{}, grpcstatus.Error(codes.FailedPrecondition, "deleted messages cannot be pinned")
	}

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Message{}, grpcstatus.Error(codes.Unavailable, "WhatsApp client is not logged in")
	}

	chatJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		return appstore.Message{}, grpcstatus.Errorf(codes.InvalidArgument, "invalid chat_id: %v", err)
	}

	now := time.Now()
	if pinned {
		alreadyPinned := message.PinnedUntil > now.Unix()
		if !alreadyPinned {
			count, err := c.store.CountActivePins(ctx, message.ChatID)
			if err != nil {
				return appstore.Message{}, err
			}
			if count >= maxPinnedMessages {
				return appstore.Message{}, grpcstatus.Errorf(codes.ResourceExhausted, "You can only pin %d messages per chat", maxPinnedMessages)
			}
		}
		if durationSecs == 0 {
			durationSecs = defaultPinDurationSecs
		}
	}

	fromMe := message.Direction == appstore.DirectionOutgoing
	var targetSender types.JID
	if !fromMe && message.SenderID != "" && message.SenderID != "me" {
		if parsed, parseErr := types.ParseJID(message.SenderID); parseErr == nil {
			targetSender = parsed
		}
	}
	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))

	pinType := waE2E.PinInChatMessage_PIN_FOR_ALL
	if !pinned {
		pinType = waE2E.PinInChatMessage_UNPIN_FOR_ALL
	}
	pinMsg := &waE2E.Message{
		PinInChatMessage: &waE2E.PinInChatMessage{
			Key:               client.BuildMessageKey(chatJID, targetSender, externalID),
			Type:              pinType.Enum(),
			SenderTimestampMS: proto.Int64(now.UnixMilli()),
		},
	}
	if pinned {
		pinMsg.MessageContextInfo = &waE2E.MessageContextInfo{
			MessageAddOnDurationInSecs: proto.Uint32(durationSecs),
			MessageAddOnExpiryType:     waE2E.MessageContextInfo_STATIC.Enum(),
		}
	}
	if _, err := client.SendMessage(ctx, chatJID, pinMsg); err != nil {
		return appstore.Message{}, grpcstatus.Errorf(codes.Unknown, "send pin failed: %v", err)
	}

	var pinnedAt, pinnedUntil int64
	if pinned {
		pinnedAt = now.Unix()
		pinnedUntil = now.Unix() + int64(durationSecs)
	}
	updated, changed, err := c.store.SetMessagePinned(ctx, message.ID, pinnedAt, pinnedUntil)
	if err != nil {
		return appstore.Message{}, err
	}
	if changed {
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	}
	return updated, nil
}

// messageSenderJID resolves the participant JID for app-state mutations that key
// off a message: our own JID for messages we sent, the original author for
// incoming group messages, and the chat itself for incoming 1:1 messages (which
// BuildStar normalizes to the self-marker "0").
func messageSenderJID(client *whatsmeow.Client, message appstore.Message, chatJID types.JID, fromMe bool) types.JID {
	if !fromMe && message.SenderID != "" && message.SenderID != "me" {
		if parsed, err := types.ParseJID(message.SenderID); err == nil {
			return parsed
		}
	}
	if fromMe && client.Store.ID != nil {
		return *client.Store.ID
	}
	return chatJID
}

// handleStarEvent applies a star/unstar made on another device (or during app
// state sync) to the local store and publishes the change.
func (c *Client) handleStarEvent(ctx context.Context, evt *events.Star) {
	if evt == nil || evt.Action == nil || evt.ChatJID.IsEmpty() {
		return
	}
	messageID := strings.TrimSpace(evt.MessageID)
	if messageID == "" {
		return
	}

	chatJID := c.normalizeJIDForChat(ctx, evt.ChatJID)
	internalID := internalMessageIDForChat(chatJID.String(), types.MessageID(messageID))

	updated, changed, err := c.store.SetMessageStarred(ctx, internalID, evt.Action.GetStarred())
	if err != nil {
		// The star may reference a message we have not synced yet; ignore quietly.
		if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warnf("Failed to apply star to message %s: %v", internalID, err)
		}
		return
	}
	if changed && !evt.FromFullSync {
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	}
}

// handlePinInChat intercepts pin-for-all / unpin-for-all messages and updates
// the referenced message's pin window instead of ingesting the control message
// as a chat message. Returns true when the event was a pin action.
func (c *Client) handlePinInChat(ctx context.Context, evt *events.Message, offlineSync bool) bool {
	if evt == nil || evt.Message == nil {
		return false
	}
	pin := evt.Message.GetPinInChatMessage()
	if pin == nil || pin.GetKey() == nil {
		return false
	}

	chatID, _ := c.internalMessageIDFromInfo(ctx, evt.Info)
	targetID := strings.TrimSpace(pin.GetKey().GetID())
	if chatID == "" || targetID == "" {
		return true
	}
	internalID := internalMessageIDForChat(chatID, types.MessageID(targetID))

	pinned := pin.GetType() == waE2E.PinInChatMessage_PIN_FOR_ALL
	var pinnedAt, pinnedUntil int64
	if pinned {
		pinnedAt = evt.Info.Timestamp.Unix()
		if ms := pin.GetSenderTimestampMS(); ms > 0 {
			pinnedAt = ms / 1000
		}
		duration := int64(evt.Message.GetMessageContextInfo().GetMessageAddOnDurationInSecs())
		if duration <= 0 {
			duration = defaultPinDurationSecs
		}
		pinnedUntil = pinnedAt + duration
	}

	updated, changed, err := c.store.SetMessagePinned(ctx, internalID, pinnedAt, pinnedUntil)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warnf("Failed to apply pin to message %s: %v", internalID, err)
		}
		return true
	}
	if changed && !offlineSync {
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	}
	return true
}

// sendRegularHighAppState mirrors sendRegularLowAppState for the regular_high
// collection (where stars live), resyncing and retrying once on a hash
// conflict.
func (c *Client) sendRegularHighAppState(ctx context.Context, client *whatsmeow.Client, patch appstate.PatchInfo) error {
	c.appStateMu.Lock()
	defer c.appStateMu.Unlock()

	if err := client.SendAppState(ctx, patch); err != nil {
		if !isAppStateConflictError(err) {
			return err
		}
		c.log.Warnf("WhatsApp app state conflict while updating stars; resyncing regular_high and retrying: %v", err)
		if _, syncErr := fetchFullRegularHighAppState(ctx, client); syncErr != nil {
			return grpcstatus.Errorf(codes.Aborted, "WhatsApp sync conflict. Try again in a moment.")
		}
		if retryErr := client.SendAppState(ctx, patch); retryErr != nil {
			return grpcstatus.Errorf(codes.Aborted, "WhatsApp sync conflict. Try again in a moment.")
		}
	}
	return nil
}

func fetchFullRegularHighAppState(ctx context.Context, client *whatsmeow.Client) ([]any, error) {
	oldEmit := client.EmitAppStateEventsOnFullSync
	client.EmitAppStateEventsOnFullSync = true
	defer func() { client.EmitAppStateEventsOnFullSync = oldEmit }()

	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return client.DangerousInternals().FetchAppState(fetchCtx, appstate.WAPatchRegularHigh, true, false)
}
