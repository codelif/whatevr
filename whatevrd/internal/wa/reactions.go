package wa

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// handleReaction intercepts emoji-reaction messages (plain or encrypted) and
// records them against their target message instead of ingesting them as chat
// messages. Returns true when the event was a reaction.
func (c *Client) handleReaction(ctx context.Context, evt *events.Message, offlineSync bool) bool {
	if evt == nil || evt.Message == nil {
		return false
	}

	reaction := evt.Message.GetReactionMessage()
	if reaction == nil {
		if evt.Message.GetEncReactionMessage() == nil {
			return false
		}
		// Group reactions arrive encrypted with the same message-secret scheme
		// as polls; decrypt them back into a plain ReactionMessage.
		client := c.currentClient()
		if client == nil {
			return true
		}
		decrypted, err := client.DecryptReaction(ctx, evt)
		if err != nil {
			c.log.Warnf("Failed to decrypt reaction: %v", err)
			return true
		}
		reaction = decrypted
	}
	if reaction == nil || reaction.GetKey() == nil {
		return true
	}

	chatID, _ := c.internalMessageIDFromInfo(ctx, evt.Info)
	targetID := strings.TrimSpace(reaction.GetKey().GetID())
	if chatID == "" || targetID == "" {
		return true
	}
	internalID := internalMessageIDForChat(chatID, types.MessageID(targetID))

	reactorName := ""
	if !evt.Info.IsFromMe {
		reactorName = strings.TrimSpace(evt.Info.PushName)
	}
	timestamp := evt.Info.Timestamp
	if ms := reaction.GetSenderTimestampMS(); ms > 0 {
		timestamp = time.UnixMilli(ms)
	}

	c.applyReaction(ctx, internalID, senderID(evt.Info), reactorName, reaction.GetText(), timestamp, evt.Info.IsFromMe, offlineSync)
	return true
}

// applyReaction persists one reactor's reaction (empty emoji removes it),
// publishes the updated message for live events, and notifies when someone else
// reacts to one of our own messages.
func (c *Client) applyReaction(ctx context.Context, internalID, reactorID, reactorName, emoji string, timestamp time.Time, fromMe, silent bool) {
	message, chat, changed, err := c.store.SaveReaction(ctx, internalID, reactorID, reactorName, emoji, timestamp.Unix(), fromMe)
	if err != nil {
		// A reaction can arrive for a message we have not synced yet; drop it
		// quietly (it ships with the message during history sync).
		if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warnf("Failed to save reaction on %s: %v", internalID, err)
		}
		return
	}
	if !changed || silent {
		return
	}

	c.daemon.PublishMessageUpdated(toDaemonMessage(message))
	c.daemon.PublishChatUpdated(toDaemonChat(chat))

	if !fromMe && emoji != "" && message.Direction == appstore.DirectionOutgoing && c.notifier != nil &&
		notificationTimestampFresh(timestamp.Unix(), time.Now()) && c.ShouldNotifyChat(chat.ID) &&
		!chatNotificationsMuted(chat.IsMuted, chat.MuteEndTimestamp) {
		if opts, enabled := c.notificationOptions(); enabled {
			c.notifyWithAvatar(ctx, reactionNotification(message, reactorID, reactorName, emoji), toDaemonChat(chat), opts)
		}
	}
}

// reactionNotification builds a synthetic incoming message so the existing
// notification formatter renders "Reacted <emoji> to your message" (prefixed
// with the reactor's name in groups, where SenderID drives the prefix).
func reactionNotification(target appstore.Message, reactorID, reactorName, emoji string) app.Message {
	return app.Message{
		ChatID:        target.ChatID,
		SenderID:      reactorID,
		SenderName:    reactorName,
		Text:          "Reacted " + emoji + " to your message",
		TimestampUnix: time.Now().Unix(),
		Direction:     appstore.DirectionIncoming,
	}
}

// saveHistoryReactions records the reactions attached to a history-synced
// message. History ingestion is silent, so these surface on the next chat open
// via the reaction-aware read queries rather than a live MessageUpdated.
func (c *Client) saveHistoryReactions(ctx context.Context, internalID, chatID string, isGroup bool, reactions []*waWeb.Reaction) {
	// History reactions carry no push name (unlike live events), so resolve the
	// reactor's display name from the contact store; cached because a synced
	// conversation often repeats the same few reactors.
	nameByReactor := make(map[string]string)
	for _, reaction := range reactions {
		emoji := strings.TrimSpace(reaction.GetText())
		if emoji == "" {
			continue
		}
		key := reaction.GetKey()
		if key == nil {
			continue
		}
		timestamp := time.Now()
		if ms := reaction.GetSenderTimestampMS(); ms > 0 {
			timestamp = time.UnixMilli(ms)
		}
		reactorID, fromMe := reactorIdentityFromKey(key, chatID, isGroup)
		reactorName := ""
		if !fromMe {
			cached, ok := nameByReactor[reactorID]
			if !ok {
				if jid, err := types.ParseJID(reactorID); err == nil {
					cached = c.senderName(ctx, jid)
				}
				nameByReactor[reactorID] = cached
			}
			reactorName = cached
		}
		c.applyReaction(ctx, internalID, reactorID, reactorName, emoji, timestamp, fromMe, true)
	}
}

// FillReactionSenderNames resolves display names for reactions stored without
// one (history syncs before name resolution existed) and persists what it finds,
// so rows heal permanently the first time they are served.
func (c *Client) FillReactionSenderNames(ctx context.Context, messages []appstore.Message) []appstore.Message {
	var nameByReactor map[string]string
	for mi := range messages {
		for ri := range messages[mi].Reactions {
			reaction := &messages[mi].Reactions[ri]
			if reaction.SenderName != "" || reaction.FromMe ||
				reaction.SenderID == "" || reaction.SenderID == "me" {
				continue
			}
			if nameByReactor == nil {
				nameByReactor = make(map[string]string)
			}
			name, ok := nameByReactor[reaction.SenderID]
			if !ok {
				if jid, err := types.ParseJID(reaction.SenderID); err == nil {
					name = c.senderName(ctx, jid)
				}
				nameByReactor[reaction.SenderID] = name
			}
			if name == "" {
				continue
			}
			reaction.SenderName = name
			if err := c.store.UpdateReactionSenderName(ctx, messages[mi].ID, reaction.SenderID, name); err != nil {
				c.log.Warnf("Failed to backfill reaction sender name on %s: %v", messages[mi].ID, err)
			}
		}
	}
	return messages
}

// reactorIdentityFromKey derives the reactor's internal id and from-me flag from
// a reaction's message key. In groups the reactor is the key participant; in
// one-to-one chats the only possible non-self reactor is the chat peer.
func reactorIdentityFromKey(key *waCommon.MessageKey, chatID string, isGroup bool) (string, bool) {
	if key.GetFromMe() {
		return "me", true
	}
	if isGroup {
		if participant := strings.TrimSpace(key.GetParticipant()); participant != "" {
			if jid, err := types.ParseJID(participant); err == nil {
				return bareAvatarJID(jid).String(), false
			}
		}
	}
	return chatID, false
}

// SendReaction reacts to a message with the given emoji (an empty emoji removes
// our existing reaction), mirroring our own reaction into the local store and
// returning the reloaded target message.
func (c *Client) SendReaction(ctx context.Context, messageID, emoji string) (appstore.Message, error) {
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return appstore.Message{}, err
	}
	if message.IsRevoked {
		return appstore.Message{}, app.NewCommandError(app.CommandErrorRejected, "deleted messages cannot be reacted to")
	}

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Message{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}

	chatJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		return appstore.Message{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}

	// The target message's sender drives the reaction key: empty for our own
	// messages (FromMe), the original sender for messages from others.
	var targetSender types.JID
	if message.Direction == appstore.DirectionIncoming && message.SenderID != "" && message.SenderID != "me" {
		if parsed, parseErr := types.ParseJID(message.SenderID); parseErr == nil {
			targetSender = parsed
		}
	}

	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))
	if _, err := client.SendMessage(ctx, chatJID, client.BuildReaction(chatJID, targetSender, externalID, emoji)); err != nil {
		return appstore.Message{}, app.NewCommandError(app.CommandErrorRejected, "send reaction failed: %v", err)
	}

	updated, chat, changed, err := c.store.SaveReaction(ctx, message.ID, "me", "", emoji, time.Now().Unix(), true)
	if err != nil {
		return appstore.Message{}, err
	}
	if changed {
		c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return updated, nil
}
