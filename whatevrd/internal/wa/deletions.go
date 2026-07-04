package wa

import (
	"context"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Phone-side deletion sync. These app-state events arrive when the user
// deletes a message ("delete for me"), clears a chat or deletes a chat on
// another device. Full-sync replays are skipped for the chat-level wipes: at
// first login the full app-state sync can carry historical clear/delete
// actions that would otherwise erase freshly history-synced transcripts.

func (c *Client) handleDeleteForMeEvent(ctx context.Context, evt *events.DeleteForMe) {
	chatID := c.normalizeJIDForChat(ctx, evt.ChatJID).String()
	if chatID == "" || evt.MessageID == "" {
		return
	}
	internalID := internalMessageIDForChat(chatID, types.MessageID(evt.MessageID))
	message, chat, existed, err := c.store.DeleteMessageForMe(ctx, internalID)
	if err != nil {
		c.log.Warnf("Failed to delete message %s for phone-side delete-for-me: %v", internalID, err)
		return
	}
	if !existed {
		// The deletion may reference a message we never synced; ignore quietly.
		return
	}
	c.log.Infof("Deleted message %s after phone-side delete-for-me", internalID)
	if !evt.FromFullSync {
		c.daemon.PublishMessageDeleted(message.ChatID, message.ID, toDaemonChat(chat))
	}
}

func (c *Client) handleDeleteChatEvent(ctx context.Context, evt *events.DeleteChat) {
	if evt.FromFullSync {
		return
	}
	chatID := c.normalizeJIDForChat(ctx, evt.JID).String()
	if chatID == "" {
		return
	}
	existed, err := c.store.DeleteChat(ctx, chatID)
	if err != nil {
		c.log.Warnf("Failed to delete chat %s for phone-side deletion: %v", chatID, err)
		return
	}
	if !existed {
		return
	}
	c.log.Infof("Deleted chat %s after phone-side deletion", chatID)
	c.daemon.PublishChatDeleted(chatID)
}

func (c *Client) handleClearChatEvent(ctx context.Context, evt *events.ClearChat) {
	if evt.FromFullSync {
		return
	}
	chatID := c.normalizeJIDForChat(ctx, evt.JID).String()
	if chatID == "" {
		return
	}
	chat, existed, err := c.store.ClearChatMessages(ctx, chatID)
	if err != nil {
		c.log.Warnf("Failed to clear chat %s for phone-side clear: %v", chatID, err)
		return
	}
	if !existed {
		return
	}
	c.log.Infof("Cleared chat %s after phone-side clear", chatID)
	c.daemon.PublishChatCleared(toDaemonChat(chat))
}
