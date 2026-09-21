package wa

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// This file implements the honest slice of WhatsApp calls a companion client
// can offer: whatsmeow exposes call signaling with no media stack (see
// feature-gap §17), so answering or placing calls from the desktop is
// upstream-impossible. What we do: surface incoming calls the moment they
// ring, let the user reject them, and leave a missed-call tombstone when one
// goes unanswered. Audio/video calls are therefore "supported" the way group
// chats were at first: visible, actionable, and clearly labeled.

// pendingCall is one locally-ringing call: enough to render the `calls` view,
// reject it, or tombstone it as missed when the other end hangs up.
type pendingCall struct {
	callID   string
	chatID   string
	from     types.JID
	video    bool
	group    bool
	started  time.Time
	rejected bool
}

func (c *Client) pendingCallKey(callID string) string {
	return strings.TrimSpace(callID)
}

// trackPendingCall records a ringing call and publishes it. Callers hold no
// locks; the map is guarded internally.
func (c *Client) trackPendingCall(call pendingCall) {
	if call.callID == "" {
		return
	}
	c.callsMu.Lock()
	if c.pendingCalls == nil {
		c.pendingCalls = make(map[string]*pendingCall)
	}
	c.pendingCalls[call.callID] = &call
	c.callsMu.Unlock()
	c.daemon.SetRingingCall(app.RingingCall{
		CallID:      call.callID,
		ChatID:      call.chatID,
		CallerID:    call.from.ToNonAD().String(),
		Video:       call.video,
		StartedUnix: call.started.Unix(),
	})
	c.daemon.PublishCallChanged(call.callID, call.chatID)
}

// dropPendingCall forgets a ringing call (answered elsewhere, rejected,
// terminated) and publishes the change. It reports the call, if any.
func (c *Client) dropPendingCall(callID string) *pendingCall {
	callID = strings.TrimSpace(callID)
	var call *pendingCall
	c.callsMu.Lock()
	if c.pendingCalls != nil {
		call = c.pendingCalls[callID]
		delete(c.pendingCalls, callID)
	}
	c.callsMu.Unlock()
	if call != nil {
		c.daemon.ClearRingingCall(call.callID)
		c.daemon.PublishCallChanged(call.callID, call.chatID)
	}
	return call
}

func (c *Client) pendingCallForChat(chatID string) *pendingCall {
	var latest *pendingCall
	c.callsMu.Lock()
	defer c.callsMu.Unlock()
	for _, call := range c.pendingCalls {
		if call.chatID == chatID && (latest == nil || call.started.After(latest.started)) {
			latest = call
		}
	}
	return latest
}

// handleCallOffer rings a 1:1 incoming call: ensure the chat row (so the
// notification can open it), record it for the `calls` view, and notify
// "answer on your phone" — the desktop has no media stack to pick up with.
func (c *Client) handleCallOffer(ctx context.Context, evt *events.CallOffer) {
	if evt == nil || evt.CallID == "" || evt.From.IsEmpty() {
		return
	}
	from := c.normalizeJIDForChat(ctx, evt.From.ToNonAD())
	chatID := from.String()
	name, _ := c.displayNameForChat(ctx, from, false, "", "")
	chat, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, "", false)
	if err != nil {
		c.log.Warnf("Failed to ensure call chat %s: %v", chatID, err)
		return
	}
	c.trackPendingCall(pendingCall{
		callID:  evt.CallID,
		chatID:  chatID,
		from:    from,
		started: time.Now(),
	})
	c.daemon.PublishChatUpdated(toDaemonChat(chat))
	c.notifyCall(ctx, chat, "📞 Incoming voice call — answer on your phone")
}

// handleCallOfferNotice rings an incoming group call.
func (c *Client) handleCallOfferNotice(ctx context.Context, evt *events.CallOfferNotice) {
	if evt == nil || evt.CallID == "" {
		return
	}
	chatJID := c.normalizeJIDForChat(ctx, evt.GroupJID)
	if chatJID.Server != types.GroupServer {
		return
	}
	chatID := chatJID.String()
	video := strings.EqualFold(strings.TrimSpace(evt.Media), "video")
	name, _ := c.displayNameForChat(ctx, chatJID, true, "", "")
	chat, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, appstore.ChatNameSourceGroup, true)
	if err != nil {
		c.log.Warnf("Failed to ensure group call chat %s: %v", chatID, err)
		return
	}
	c.trackPendingCall(pendingCall{
		callID:  evt.CallID,
		chatID:  chatID,
		from:    c.normalizeJIDForChat(ctx, evt.From.ToNonAD()),
		video:   video,
		group:   true,
		started: time.Now(),
	})
	c.daemon.PublishChatUpdated(toDaemonChat(chat))
	label := "📞 Incoming group voice call — answer on your phone"
	if video {
		label = "📹 Incoming group video call — answer on your phone"
	}
	c.notifyCall(ctx, chat, label)
}

// handleCallTerminate tombstones an unanswered ringing call as missed. A call
// we rejected needs no tombstone: the user already handled it.
func (c *Client) handleCallTerminate(ctx context.Context, evt *events.CallTerminate) {
	if evt == nil || evt.CallID == "" {
		return
	}
	call := c.dropPendingCall(evt.CallID)
	if call == nil || call.rejected {
		return
	}
	label := "📞 Missed voice call"
	if call.video {
		label = "📹 Missed video call"
	} else if call.group {
		label = "📞 Missed group call"
	}
	saved, err := c.store.SaveTextMessage(ctx, appstore.TextMessageInput{
		ID:          internalMessageIDForChat(call.chatID, callIDMessageID(evt.CallID)),
		ChatID:      call.chatID,
		SenderID:    call.from.ToNonAD().String(),
		Timestamp:   time.Now(),
		Direction:   appstore.DirectionIncoming,
		Status:      appstore.StatusDelivered,
		IsGroup:     call.group,
		CountUnread: true,
		Text:        label,
	})
	if err != nil {
		c.log.Warnf("Failed to store missed call %s: %v", evt.CallID, err)
		return
	}
	if saved.Inserted {
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
		c.notifyCall(ctx, saved.Chat, label)
	}
}

// handleCallReject drops a remotely-rejected call without a tombstone.
func (c *Client) handleCallReject(ctx context.Context, evt *events.CallReject) {
	if evt == nil || evt.CallID == "" {
		return
	}
	c.dropPendingCall(evt.CallID)
}

// RejectCall rejects the latest ringing call in a chat, if any. Rejecting a
// call that already ended is a silent no-op (checked before connectivity so
// an empty ring state never errors).
func (c *Client) RejectCall(ctx context.Context, chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "chat_id is required")
	}
	call := c.pendingCallForChat(chatID)
	if call == nil {
		return nil
	}
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	if err := client.RejectCall(ctx, call.from, call.callID); err != nil {
		return err
	}
	c.callsMu.Lock()
	if c.pendingCalls != nil {
		if tracked, ok := c.pendingCalls[call.callID]; ok {
			tracked.rejected = true
		}
	}
	c.callsMu.Unlock()
	c.dropPendingCall(call.callID)
	return nil
}

// notifyCall sends a display-only notification: unlike chat messages, calls
// have no stored row at ring time, so this crafts the popup directly.
func (c *Client) notifyCall(ctx context.Context, chat appstore.Chat, label string) {
	if c.notifier == nil {
		return
	}
	opts, enabled := c.notificationOptions()
	if !enabled || !c.ShouldNotifyChat(chat.ID) || chatNotificationsMuted(chat.IsMuted, chat.MuteEndTimestamp) {
		return
	}
	daemonChat := toDaemonChat(chat)
	c.notifyWithAvatar(ctx, app.Message{
		ID:            "call:" + daemonChat.ID,
		ChatID:        daemonChat.ID,
		SenderID:      daemonChat.ID,
		Text:          label,
		TimestampUnix: time.Now().Unix(),
		Direction:     appstore.DirectionIncoming,
	}, daemonChat, opts)
}

// callIDMessageID derives a stable internal message suffix from a call id.
func callIDMessageID(callID string) string {
	return "call-" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, callID)
}
