package wa

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// ingestSource distinguishes a freshly-received WhatsApp message from a
// history-sync backfill. The two paths share storage logic but diverge on
// notification, event publication, and status mapping.
type ingestSource int

const (
	sourceLive ingestSource = iota
	sourceHistorySync
	sourceOfflineSync
)

const liveNotificationMaxAge = 2 * time.Minute
const historySyncProgressInterval = 250 * time.Millisecond
const undecryptableMessageRetention = 7 * 24 * time.Hour

type ingestOptions struct {
	source           ingestSource
	chatNameOverride string
	chatNameSource   string
	// historyStatus is set only for sourceHistorySync; mapped from
	// WebMessageInfo.Status. Empty string means "no override".
	historyStatus string
	// forceRead, when true, suppresses unread counting for this message.
	// Used during history sync when the conversation is already read on
	// the phone — we don't want to spuriously bump unread badges that
	// the user already cleared.
	forceRead         bool
	timestampOverride time.Time
}

func (c *Client) handleHistorySync(sess *accountSession, evt *events.HistorySync) {
	if !sess.alive() {
		return
	}
	ctx := sess.detached()
	if ctx.Err() != nil {
		return
	}
	c.processHistorySyncData(ctx, evt.Data)
}

func (c *Client) processHistorySyncData(ctx context.Context, data *waHistorySync.HistorySync) bool {
	client := c.currentClient()
	if client == nil || data == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	c.updateChatNamesFromHistorySync(ctx, &events.HistorySync{Data: data})
	c.ingestRecentStickers(ctx, data.GetRecentStickers())

	syncType := historySyncType(data.GetSyncType())
	progressPercent := data.GetProgress()
	chunkOrder := data.GetChunkOrder()
	conversations := data.GetConversations()
	totalMessages := uint32(0)
	for _, conv := range conversations {
		totalMessages += uint32(len(conv.GetMessages()))
	}

	initialEvt := app.HistorySyncEvent{
		SyncType:             syncType,
		ProgressPercent:      progressPercent,
		ChunkOrder:           chunkOrder,
		ConversationsInChunk: uint32(len(conversations)),
		MessagesInChunk:      totalMessages,
		IsComplete:           false,
		Phase:                app.HistorySyncPhaseProcessing,
	}
	c.noteHistorySyncActivity(initialEvt)
	c.daemon.PublishHistorySyncProgress(initialEvt)
	processedConversations := uint32(0)
	processedMessages := uint32(0)
	lastProgressPublish := time.Now()
	publishProcessingProgress := func(force bool) {
		if !force && time.Since(lastProgressPublish) < historySyncProgressInterval {
			return
		}
		lastProgressPublish = time.Now()
		evt := app.HistorySyncEvent{
			SyncType:               syncType,
			ProgressPercent:        progressPercent,
			ChunkOrder:             chunkOrder,
			ConversationsInChunk:   uint32(len(conversations)),
			MessagesInChunk:        totalMessages,
			IsComplete:             false,
			Phase:                  app.HistorySyncPhaseProcessing,
			ProcessedConversations: processedConversations,
			ProcessedMessages:      processedMessages,
		}
		c.noteHistorySyncActivity(evt)
		c.daemon.PublishHistorySyncProgress(evt)
	}

	// On-demand responses resolve their originating backfill request by
	// comparing how many messages the phone returned per chat (backfill.go).
	var onDemandCounts map[string]int
	if syncType == app.HistorySyncTypeOnDemand {
		onDemandCounts = make(map[string]int, len(conversations))
	}

	// Chats the phone gave a straight answer about, so the guess in
	// resolveBackfillRequests does not get to overrule it.
	answered := make(map[string]bool, len(conversations))

	// A conversation that failed to store must not let its chunk be marked
	// processed: the chunk is pruned after that, and the server was acked
	// before ingestion even started, so the backfill would be gone for good.
	// Only storage failures count; an unparseable JID will not store on a
	// retry either, so it does not burn the attempt budget.
	stored := true

	for _, conv := range conversations {
		if ctx.Err() != nil {
			return false
		}
		rawChatJID, err := types.ParseJID(conv.GetID())
		if err != nil {
			c.log.Warnf("Failed to parse chat JID in history sync: %v", err)
			continue
		}
		chatJID := c.normalizeJIDForChat(ctx, rawChatJID)
		chatNameOverride := ""
		chatNameSource := ""
		if chatJID.Server == types.GroupServer {
			chatNameOverride = historySyncChatName(conv)
			chatNameSource = appstore.ChatNameSourceGroup
		} else {
			chatNameOverride, chatNameSource = c.displayNameForChat(ctx, chatJID, false, "", "")
		}
		chatID := chatJID.String()
		if onDemandCounts != nil {
			onDemandCounts[chatID] += len(conv.GetMessages())
		}
		if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, chatNameOverride, chatNameSource, chatJID.Server == types.GroupServer); err != nil {
			if ctx.Err() != nil {
				return false
			}
			c.log.Warnf("Failed to ensure history-sync chat %s: %v", chatID, err)
			stored = false
			continue
		}
		convUnread := effectiveHistorySyncUnread(conv)
		convMarkedUnread := conv.GetMarkedAsUnread()
		convHasPinned, convPinned, convPinnedOrder := historySyncConversationPinState(conv)
		forceRead := convUnread == 0 && !convMarkedUnread

		// Phase 1: parse every message and build its storage input. No app-DB
		// writes happen here, so the batched save below can hold the single
		// write transaction without anything else contending for it.
		type historySaveItem struct {
			item          appstore.MessageSaveItem
			id            string
			historyStatus string
			isMedia       bool
			reactions     []*waWeb.Reaction
			// the parsed proto and its timestamp are what the post-save
			// registrations need, so they have to survive phase 1
			waMsg     *waE2E.Message
			timestamp time.Time
			// keptKnown separates "this message says nothing about keeping" from
			// "this message says it is not kept", because only the second should
			// clear a flag a live keep already set.
			keptKnown bool
			kept      bool
		}
		pending := make([]historySaveItem, 0, len(conv.GetMessages()))
		for _, msg := range conv.GetMessages() {
			if ctx.Err() != nil {
				return false
			}
			processedMessages++
			webMsg := msg.GetMessage()
			if webMsg == nil {
				publishProcessingProgress(false)
				continue
			}
			// A stub carries no message at all, so it parses into nothing and
			// matches no builder. Routed here it becomes the same pill the live
			// path writes, which is the only record a backfilled group has of
			// who joined and who left.
			if payload, ts, ok := c.historyStubSystemPayload(ctx, webMsg); ok {
				c.recordSystemEvent(ctx, chatJID, payload, ts, false)
				publishProcessingProgress(false)
				continue
			}
			parsedEvt, err := client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				c.log.Warnf("Failed to parse history sync message: %v", err)
				publishProcessingProgress(false)
				continue
			}
			// ParseWebMessage bypasses whatsmeow's own storeMessageSecret, which
			// only runs on the live path. Without the secret a poll that arrived
			// through backfill can never have its votes decrypted, so capture it
			// here where the parsed message is in hand.
			c.storeBackfilledMessageSecret(ctx, parsedEvt)
			// After the secret, which rides on the outermost wrapper.
			if parsedEvt.Message != nil {
				parsedEvt.Message = unwrapNestedMessage(parsedEvt.Message)
			}
			opts := ingestOptions{
				source:           sourceHistorySync,
				chatNameOverride: chatNameOverride,
				chatNameSource:   chatNameSource,
				historyStatus:    mapWebMessageStatus(webMsg),
				forceRead:        forceRead,
			}
			kept, keptKnown := historyKeepState(webMsg)
			if textInput, ok := c.textMessageInput(ctx, parsedEvt, opts); ok {
				input := textInput
				input.SenderDevice = parsedEvt.Info.Sender.Device
				pending = append(pending, historySaveItem{
					item:          appstore.MessageSaveItem{Text: &input},
					id:            input.ID,
					historyStatus: opts.historyStatus,
					reactions:     webMsg.GetReactions(),
					keptKnown:     keptKnown,
					kept:          kept,
				})
			} else if mediaInput, ok := c.mediaMessageInput(ctx, parsedEvt, opts); ok {
				input := mediaInput
				input.SenderDevice = parsedEvt.Info.Sender.Device
				pending = append(pending, historySaveItem{
					item:          appstore.MessageSaveItem{Media: &input},
					id:            input.ID,
					historyStatus: opts.historyStatus,
					isMedia:       true,
					reactions:     webMsg.GetReactions(),
					keptKnown:     keptKnown,
					kept:          kept,
					waMsg:         parsedEvt.Message,
					timestamp:     parsedEvt.Info.Timestamp,
				})
			}
			publishProcessingProgress(false)
		}

		// Phase 2: persist the whole conversation in one transaction.
		messagesAdded := uint32(0)
		var lastSavedChat appstore.Chat
		items := make([]appstore.MessageSaveItem, len(pending))
		for i := range pending {
			items[i] = pending[i].item
		}
		savedBatch, err := c.store.SaveMessages(ctx, items)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			c.log.Errorf("Failed to store history sync conversation %s: %v", chatID, err)
			stored = false
			continue
		}

		// Phase 3: post-save work that must stay outside the batch
		// transaction (status corrections and sticker cache resolution do
		// their own store writes).
		for i, saved := range savedBatch {
			if ctx.Err() != nil {
				return false
			}
			entry := pending[i]
			if len(entry.reactions) > 0 {
				c.saveHistoryReactions(ctx, entry.id, chatID, chatJID.Server == types.GroupServer, entry.reactions)
			}
			// Backfill is the authority on keeps, so this runs before the
			// already-here shortcut below: a message we synced yesterday and the
			// phone kept today arrives as an unchanged row carrying a new flag.
			if entry.keptKnown {
				c.applyKeepInChat(ctx, entry.id, entry.kept, false)
			}
			if !saved.Inserted {
				if entry.historyStatus != "" {
					c.maybeUpdateStatusFromHistory(ctx, entry.id, entry.historyStatus)
				}
				continue
			}
			if entry.isMedia {
				if updated, ok, err := c.resolveCachedStickerMedia(ctx, saved.Message); err != nil {
					c.log.Warnf("Failed to resolve cached sticker media for %s: %v", saved.Message.ID, err)
				} else if ok {
					saved.Message = updated
				}
				c.registerSavedMessage(ctx, saved.Message, entry.waMsg, entry.timestamp, false)
			}
			c.log.Debugf("Stored history message %s from %s", saved.Message.ID, saved.Message.SenderID)
			messagesAdded++
			lastSavedChat = saved.Chat
		}
		processedConversations++
		publishProcessingProgress(true)

		// Mirror the phone's read state on the chat row even if the
		// per-message rows we just inserted didn't bump it (CountUnread
		// is intentionally false during history-sync ingestion).
		if ctx.Err() != nil {
			return false
		}
		updatedChat, unreadChanged, err := c.store.OverwriteChatUnreadCount(ctx, chatID, convUnread)
		if err != nil {
			c.log.Warnf("Failed to overwrite unread count for %s: %v", chatID, err)
		} else if updatedChat.ID != "" {
			lastSavedChat = updatedChat
		}
		historyExhaustedChanged := false
		if exhausted, known := historyExhaustedFromConversation(conv); known {
			answered[chatID] = true
			if chat, changed, err := c.store.UpdateChatHistoryExhausted(ctx, chatID, exhausted); err != nil {
				c.log.Warnf("Failed to record history exhaustion for %s: %v", chatID, err)
			} else if changed {
				lastSavedChat = chat
				historyExhaustedChanged = true
			}
		}
		pinChanged := false
		if convHasPinned {
			updatedChat, pinChanged, err = c.store.UpdateChatPinState(ctx, chatID, convPinned, convPinnedOrder)
			if err != nil {
				c.log.Warnf("Failed to update pinned state for %s: %v", chatID, err)
			} else if updatedChat.ID != "" {
				lastSavedChat = updatedChat
			}
		}

		if messagesAdded > 0 || pinChanged || unreadChanged || historyExhaustedChanged {
			if lastSavedChat.ID != "" {
				c.daemon.PublishChatUpdated(toDaemonChat(lastSavedChat))
			}
			c.daemon.PublishHistoryBackfilled(chatID, messagesAdded)
		}
	}

	if ctx.Err() != nil {
		return false
	}

	if onDemandCounts != nil {
		c.resolveBackfillRequests(ctx, onDemandCounts, answered)
	}

	complete := historySyncIsComplete(syncType, progressPercent)
	finalEvt := app.HistorySyncEvent{
		SyncType:               syncType,
		ProgressPercent:        progressPercent,
		ChunkOrder:             chunkOrder,
		ConversationsInChunk:   uint32(len(conversations)),
		MessagesInChunk:        totalMessages,
		IsComplete:             complete,
		Phase:                  historySyncProgressPhase(complete),
		ProcessedConversations: processedConversations,
		ProcessedMessages:      processedMessages,
	}
	if complete {
		c.clearHistorySyncStallWatch()
	} else {
		c.noteHistorySyncActivity(finalEvt)
	}
	c.daemon.PublishHistorySyncProgress(finalEvt)
	if complete && historySyncTypeCarriesMessages(syncType) {
		// The initial sync has settled: merge LID-duplicated chats, apply any
		// app-state entries that were parked awaiting LID→PN mappings, and
		// let the background refresher start filling in chat avatars.
		c.spawn(func(ctx context.Context) {
			c.reconcileAfterHistorySync(ctx)
			c.kickAvatarBackgroundRefresh()
		})
	}

	return stored
}

// historyExhaustedFromConversation reads the phone's own answer about whether
// more history remains. WhatsApp states it outright, so nothing here has to be
// inferred from how many messages happened to arrive. known=false means the
// field was absent and the flag must be left exactly as it was.
func historyExhaustedFromConversation(conv *waHistorySync.Conversation) (bool, bool) {
	if conv == nil || conv.EndOfHistoryTransferType == nil {
		return false, false
	}
	switch conv.GetEndOfHistoryTransferType() {
	case waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY:
		return true, true
	case waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS:
		// More exist, but the phone will not hand them over. Offering to load
		// older messages that can never arrive is worse than saying there are
		// none.
		return true, true
	case waHistorySync.Conversation_COMPLETE_BUT_MORE_MESSAGES_REMAIN_ON_PRIMARY,
		waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY:
		return false, true
	}
	return false, false
}

func effectiveHistorySyncUnread(conv *waHistorySync.Conversation) uint32 {
	if conv == nil {
		return 0
	}
	unread := conv.GetUnreadCount()
	if unread == 0 && conv.GetMarkedAsUnread() {
		// WhatsApp's manual "mark unread" is a dot, not a numeric count.
		// Mirror it with a one-count badge so linked-device state is visible.
		return 1
	}
	return unread
}

func historySyncConversationPinState(conv *waHistorySync.Conversation) (present bool, pinned bool, order uint32) {
	if conv == nil || conv.Pinned == nil {
		return false, false, 0
	}
	order = conv.GetPinned()
	return true, order > 0, order
}

func (c *Client) handleMessage(ctx context.Context, evt *events.Message, offlineSync bool) bool {
	if evt != nil && evt.Message != nil {
		evt.Message = unwrapNestedMessage(evt.Message)
	}
	// Contact statuses ride the same event as chat messages but live outside
	// chats; route them before any handler that would file them as one.
	if isStatusBroadcast(evt) {
		if !offlineSync {
			c.ingestStatusUpdate(ctx, evt)
		}
		return true
	}
	// Channel posts ride the same event too but belong to the Channels tab;
	// filing them as chats would put every followed channel's feed in the
	// chat list. The channel_messages view fetches live and never stores, so
	// there is nothing to file — just nudge open channel views to refetch.
	// Any chat row the old ingest filed (or a racing writer recreated) is
	// retired on the spot.
	if evt.Info.Chat.Server == types.NewsletterServer {
		if !offlineSync {
			c.daemon.PublishChannelsChanged()
			c.notifyChannelPost(ctx, evt)
			if existed, err := c.store.DeleteChat(ctx, evt.Info.Chat.String()); err != nil {
				c.log.Warnf("Failed to retire newsletter chat %s: %v", evt.Info.Chat.String(), err)
			} else if existed {
				c.daemon.PublishChatDeleted(evt.Info.Chat.String())
			}
		}
		return true
	}
	if handled, stored := c.handleManualHistorySyncNotification(ctx, evt); handled {
		return stored
	}
	if c.handleRevokeMessage(ctx, evt, offlineSync) {
		return true
	}
	if c.handleEditMessage(ctx, evt, offlineSync) {
		return true
	}
	if c.handleReaction(ctx, evt, offlineSync) {
		return true
	}
	if c.handlePinInChat(ctx, evt, offlineSync) {
		return true
	}
	// Keeping a disappearing message marks the message it names, the same way a
	// pin does, so it never becomes a row either.
	if c.handleKeepInChat(ctx, evt, offlineSync) {
		return true
	}
	// Changing the disappearing timer in a one-to-one chat is something the chat
	// did, not something anybody said, so it becomes a pill rather than a
	// bubble.
	if c.handleEphemeralSetting(ctx, evt) {
		return true
	}
	// A live-location update moves an existing share rather than becoming a row
	// of its own, so it never reaches the ingest below.
	if c.handleLiveLocationUpdate(ctx, evt) {
		return true
	}
	// A vote changes a poll rather than adding to the conversation, so it never
	// becomes a row of its own.
	if c.handlePollUpdate(ctx, evt) {
		return true
	}
	if c.handlePollAddOption(ctx, evt) {
		return true
	}
	// An RSVP changes an event the same way, and for the same reason.
	if c.handleEventResponse(ctx, evt) {
		return true
	}
	source := sourceLive
	if offlineSync {
		source = sourceOfflineSync
	}
	opts := ingestOptions{source: source}
	if timestamp, ok := c.originalRetryTimestamp(ctx, evt); ok {
		opts.timestampOverride = timestamp
	}
	saved, inserted, stored := c.ingestMessage(ctx, evt, opts)
	// Sending from another device implies everything before it was read there;
	// clear the badge up to that message's timestamp.
	if inserted && evt.Info.IsFromMe && saved.Chat.UnreadCount > 0 {
		if chat, changed, err := c.store.MarkChatReadUpTo(ctx, saved.Chat.ID, evt.Info.Timestamp.Unix()); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				c.log.Warnf("Failed to clear unread after own message in %s: %v", saved.Chat.ID, err)
			}
		} else if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
	if offlineSync {
		c.recordOfflineSyncMessage(saved.Chat.ID, inserted)
		return stored
	}
	c.refreshRawGroupNameForChat(ctx, saved.Chat)
	c.refreshLiveMessageAvatars(ctx, evt)
	return stored
}

// handleRevokeMessage intercepts "delete for everyone" protocol messages and
// tombstones the referenced message instead of ingesting the protocol message
// as a chat message. Returns true when the event was a revoke.
func (c *Client) handleRevokeMessage(ctx context.Context, evt *events.Message, offlineSync bool) bool {
	if evt == nil || evt.Message == nil {
		return false
	}
	protocol := evt.Message.GetProtocolMessage()
	if protocol == nil || protocol.GetKey() == nil {
		return false
	}
	if protocol.GetType() != waE2E.ProtocolMessage_REVOKE {
		return false
	}

	chatID, _ := c.internalMessageIDFromInfo(ctx, evt.Info)
	targetID := strings.TrimSpace(protocol.GetKey().GetID())
	if chatID == "" || targetID == "" {
		return true
	}

	internalID := internalMessageIDForChat(chatID, types.MessageID(targetID))
	message, chat, changed, err := c.store.MarkMessageRevoked(ctx, internalID, c.appPreferences().AntiDelete)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warnf("Failed to mark message %s revoked: %v", internalID, err)
		}
		return true
	}
	if changed && !offlineSync {
		c.daemon.PublishMessageUpdated(toDaemonMessage(message))
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return true
}

// editPayload reports whether evt is a message edit and, if so, yields the id
// of the message being edited plus its replacement content. WhatsApp sends
// edits in two shapes: a bare MESSAGE_EDIT protocol message, and — what newer
// clients actually send — the same payload sealed in a SecretEncryptedMessage
// under the *target* message's secret, which whatsmeow hands over still
// encrypted. The content is nil for an edit we could not open; callers must
// still swallow such an event rather than ingest it as a chat message.
func (c *Client) editPayload(ctx context.Context, evt *events.Message) (string, *waE2E.Message, bool) {
	if enc := evt.Message.GetSecretEncryptedMessage(); enc != nil {
		if enc.GetSecretEncType() != waE2E.SecretEncryptedMessage_MESSAGE_EDIT {
			return "", nil, false
		}
		// The envelope names its target directly; the plaintext may name it
		// again in a protocol key, which wins when present.
		targetID := strings.TrimSpace(enc.GetTargetMessageKey().GetID())
		client := c.currentClient()
		if client == nil {
			return targetID, nil, true
		}
		decrypted, err := client.DecryptSecretEncryptedMessage(ctx, evt)
		if err != nil {
			c.log.Warnf("Failed to decrypt edit of message %s: %v", targetID, err)
			return targetID, nil, true
		}
		if protocol := decrypted.GetProtocolMessage(); protocol.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT {
			if id := strings.TrimSpace(protocol.GetKey().GetID()); id != "" {
				targetID = id
			}
			return targetID, protocol.GetEditedMessage(), true
		}
		return targetID, decrypted, true
	}

	protocol := evt.Message.GetProtocolMessage()
	if protocol == nil || protocol.GetKey() == nil {
		return "", nil, false
	}
	if protocol.GetType() != waE2E.ProtocolMessage_MESSAGE_EDIT {
		return "", nil, false
	}
	return strings.TrimSpace(protocol.GetKey().GetID()), protocol.GetEditedMessage(), true
}

// handleEditMessage intercepts "edit message" events and replaces the
// referenced message's body/caption in place instead of ingesting the edit as a
// new chat message. Returns true when the event was an edit. The new content
// carries the original message id, exactly as revokes do.
func (c *Client) handleEditMessage(ctx context.Context, evt *events.Message, offlineSync bool) bool {
	if evt == nil || evt.Message == nil {
		return false
	}
	targetID, content, isEdit := c.editPayload(ctx, evt)
	if !isEdit {
		return false
	}
	if content == nil {
		return true
	}

	chatID, _ := c.internalMessageIDFromInfo(ctx, evt.Info)
	if chatID == "" || targetID == "" {
		return true
	}

	// quotedReplyPreview yields the body for a text message or the caption for a
	// media message, which is exactly the field an edit replaces.
	newText, _, _ := quotedReplyPreview(content)

	// An edit re-states its mentions; pass a non-nil (possibly empty) slice so a
	// mention added or removed in the edit is reflected, never stale.
	mentions := c.mentionsFromMessage(ctx, content)
	if mentions == nil {
		mentions = []appstore.MessageMention{}
	}

	internalID := internalMessageIDForChat(chatID, types.MessageID(targetID))
	message, chat, changed, err := c.store.UpdateMessageText(ctx, internalID, newText, mentions)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warnf("Failed to apply edit to message %s: %v", internalID, err)
		}
		return true
	}
	if changed && !offlineSync {
		c.daemon.PublishMessageUpdated(toDaemonMessage(message))
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return true
}

func (c *Client) handleUndecryptableMessage(ctx context.Context, evt *events.UndecryptableMessage) {
	if evt == nil {
		return
	}
	chatID, internalID := c.internalMessageIDFromInfo(ctx, evt.Info)
	if chatID == "" || internalID == "" || evt.Info.Timestamp.IsZero() {
		return
	}

	correction, err := c.store.RecordUndecryptableMessageTimestamp(ctx, internalID, chatID, string(evt.Info.ID), senderID(evt.Info), evt.Info.Timestamp)
	if err != nil {
		c.log.Warnf("Failed to record undecryptable message timestamp for %s: %v", internalID, err)
		return
	}
	if err := c.store.PruneUndecryptableMessageTimestamps(ctx, time.Now().Add(-undecryptableMessageRetention)); err != nil {
		c.log.Warnf("Failed to prune undecryptable message timestamps: %v", err)
	}
	if correction.Changed {
		c.daemon.PublishMessageUpdated(toDaemonMessage(correction.Message))
		c.daemon.PublishChatUpdated(toDaemonChat(correction.Chat))
	}
	// The table above is retry bookkeeping and always was: it exists so a
	// resend lands at the message's original time. The transcript needs a row
	// of its own, or the hole is indistinguishable from silence.
	c.writeWaitingRow(ctx, evt)
}

func (c *Client) originalRetryTimestamp(ctx context.Context, evt *events.Message) (time.Time, bool) {
	if evt == nil || (evt.RetryCount <= 0 && evt.UnavailableRequestID == "") {
		return time.Time{}, false
	}
	_, internalID := c.internalMessageIDFromInfo(ctx, evt.Info)
	if internalID == "" {
		return time.Time{}, false
	}
	timestamp, ok, err := c.store.LookupUndecryptableMessageTimestamp(ctx, internalID)
	if err != nil {
		c.log.Warnf("Failed to look up undecryptable message timestamp for %s: %v", internalID, err)
		return time.Time{}, false
	}
	return timestamp, ok
}

func (c *Client) internalMessageIDFromInfo(ctx context.Context, info types.MessageInfo) (string, string) {
	chatJID := c.normalizeJIDForChat(ctx, info.Chat)
	chatID := chatJID.String()
	if chatID == "" || info.ID == "" {
		return "", ""
	}
	return chatID, internalMessageIDForChat(chatID, info.ID)
}

func (c *Client) refreshLiveMessageAvatars(ctx context.Context, evt *events.Message) {
	if evt == nil {
		return
	}
	chatJID := c.normalizeJIDForChat(ctx, evt.Info.Chat)
	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: bareAvatarJID(chatJID).String()}, avatarPriorityBackground)
	if evt.Info.IsGroup && !evt.Info.Sender.IsEmpty() {
		c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectSender, ID: bareAvatarJID(evt.Info.Sender).String()}, avatarPriorityBackground)
	}
}

// registerSavedMessage runs the post-save registrations that a message's later
// events depend on. Every ingest path has to call it, not just the live one:
// these build the state that arrives-later events are matched against, so a
// backfilled row that skips it is permanently inert.
//
// A live share has to be registered the moment its opening message lands,
// because the position updates that follow are matched to it by sender and
// there is nothing else tying them together. A poll's options carry the hashes
// its votes will name, so they have to be recorded before any vote can be
// matched to a choice; doing it here also drains the votes that arrived before
// the poll. An invite's card is worth more than the sender's snapshot of it, so
// the code is resolved in the background as soon as the row exists.
func (c *Client) registerSavedMessage(ctx context.Context, message appstore.Message, waMsg *waE2E.Message, timestamp time.Time, live bool) {
	switch message.MediaKind {
	case appstore.MediaKindPoll:
		if poll := pollCreationFromMessage(waMsg); poll != nil {
			c.savePollOptions(ctx, message.ID, poll)
		}
	case appstore.MediaKindGroupInvite:
		c.maybeResolveGroupInvite(ctx, message)
	case appstore.MediaKindLiveLocation:
		c.registerLiveShare(ctx, message, timestamp, 0)
		if live {
			c.daemon.PublishLiveLocationsChanged(message.ChatID)
		}
	}
	// Not in the switch: a link preview rides alongside a message whose kind
	// stays `text`, so there is no media kind to match on.
	c.maybeFetchLinkPreviewThumbnail(ctx, message, waMsg, live)
}

// ingestMessage stores a parsed whatsmeow message in the local store and,
// for live messages, publishes the daemon NewMessage event and triggers a
// desktop notification when appropriate.
//
// History-sync messages are saved silently: no NewMessage broadcast, no
// notification. The caller is responsible for emitting per-chat backfill
// events once the conversation has been processed.
// The third result reports whether the store is in the state it should be.
// It is false only when a write failed, never for a duplicate or for a payload
// nothing handles, because neither of those gets better on redelivery.
func (c *Client) ingestMessage(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.SavedTextMessage, bool, bool) {
	if textInput, ok := c.textMessageInput(ctx, evt, opts); ok {
		textInput.SenderDevice = evt.Info.Sender.Device
		saved, err := c.store.SaveTextMessage(ctx, textInput)
		if err != nil {
			c.log.Errorf("Failed to store text message %s: %v", textInput.ID, err)
			return appstore.SavedTextMessage{}, false, false
		}
		if !saved.Inserted {
			if opts.source == sourceHistorySync && opts.historyStatus != "" {
				c.maybeUpdateStatusFromHistory(ctx, textInput.ID, opts.historyStatus)
			}
			if opts.chatNameOverride != "" && opts.source == sourceLive {
				c.daemon.PublishChatUpdated(toDaemonChat(saved.Chat))
			}
			return saved, false, true
		}
		if opts.source == sourceLive {
			c.log.Infof("Stored text message %s from %s", saved.Message.ID, saved.Message.SenderID)
		} else if opts.source == sourceOfflineSync {
			c.log.Debugf("Stored offline-sync text message %s from %s", saved.Message.ID, saved.Message.SenderID)
		} else {
			c.log.Debugf("Stored history text message %s from %s", saved.Message.ID, saved.Message.SenderID)
		}
		if opts.source == sourceLive {
			message := toDaemonMessage(saved.Message)
			chat := toDaemonChat(saved.Chat)
			c.daemon.PublishNewMessage(message, chat)
			c.clearComposingAfterLiveIncomingMessage(message)
			if c.notifier != nil && c.shouldNotifyLiveMessage(message, chat, textInput.CountUnread) {
				if opts, enabled := c.notificationOptions(); enabled {
					c.notifyWithAvatar(ctx, message, chat, opts)
				}
			}
		}
		return saved, true, true
	}

	if mediaInput, ok := c.mediaMessageInput(ctx, evt, opts); ok {
		mediaInput.SenderDevice = evt.Info.Sender.Device
		saved, err := c.store.SaveMediaMessage(ctx, mediaInput)
		if err != nil {
			c.log.Errorf("Failed to store media message %s: %v", mediaInput.ID, err)
			return appstore.SavedTextMessage{}, false, false
		}
		if !saved.Inserted {
			if opts.source == sourceHistorySync && opts.historyStatus != "" {
				c.maybeUpdateStatusFromHistory(ctx, mediaInput.ID, opts.historyStatus)
			}
			if opts.chatNameOverride != "" && opts.source == sourceLive {
				c.daemon.PublishChatUpdated(toDaemonChat(saved.Chat))
			}
			return saved, false, true
		}
		if updated, ok, err := c.resolveCachedStickerMedia(ctx, saved.Message); err != nil {
			c.log.Warnf("Failed to resolve cached sticker media for %s: %v", saved.Message.ID, err)
		} else if ok {
			saved.Message = updated
		}
		c.recordInboundStickerRecency(ctx, mediaInput)
		c.registerSavedMessage(ctx, saved.Message, evt.Message, evt.Info.Timestamp, opts.source == sourceLive)
		if opts.source == sourceLive {
			c.log.Infof("Stored media message %s from %s", saved.Message.ID, saved.Message.SenderID)
		} else if opts.source == sourceOfflineSync {
			c.log.Debugf("Stored offline-sync media message %s from %s", saved.Message.ID, saved.Message.SenderID)
		} else {
			c.log.Debugf("Stored history media message %s from %s", saved.Message.ID, saved.Message.SenderID)
		}
		if opts.source == sourceLive {
			message := toDaemonMessage(saved.Message)
			chat := toDaemonChat(saved.Chat)
			// A picture that landed inside an album is not a new message on
			// screen: the album is, and it grew. Announcing the child would
			// name a row no window holds, and notifying for it would ring five
			// times for one thing somebody sent. A child whose header never
			// arrived is not grouped by anything and takes the ordinary path.
			if !c.publishAlbumChild(ctx, saved.Message) {
				c.daemon.PublishNewMessage(message, chat)
				c.clearComposingAfterLiveIncomingMessage(message)
				if c.notifier != nil && c.shouldNotifyLiveMessage(message, chat, mediaInput.CountUnread) {
					if opts, enabled := c.notificationOptions(); enabled {
						c.notifyWithAvatar(ctx, message, chat, opts)
					}
				}
			}
			// Media is no longer downloaded eagerly on receipt. The frontend
			// requests downloads lazily, when a message scrolls into view and
			// the user's auto-download policy covers its kind (see ChatBubble).
		}
		return saved, true, true
	}

	return appstore.SavedTextMessage{}, false, true
}

// recordInboundStickerRecency files a received sticker in the local Recents
// library, so stickers arriving from the phone (or any sender) show up in
// the picker the way the phone's own recents do. Own sends are already
// recorded by SendSticker, and history sync seeds recents with weights, so
// only inbound live/offline rows land here. The row is created when needed
// (files download lazily on first picker display); existing rows only move
// up via last_used, and phone-initiated removals still win through
// ClearStickerRecency.
func (c *Client) recordInboundStickerRecency(ctx context.Context, input appstore.MediaMessageInput) {
	if input.MediaKind != appstore.MediaKindSticker || input.MediaCacheKey == "" {
		return
	}
	if input.Direction != appstore.DirectionIncoming {
		return
	}
	if err := c.store.TouchRecentSticker(ctx, appstore.Sticker{
		CacheKey:       input.MediaCacheKey,
		MimeType:       input.MediaMimeType,
		IsAnimated:     input.MediaAnimated,
		Width:          input.MediaWidth,
		Height:         input.MediaHeight,
		StickerPayload: input.MediaPayload,
		LastUsed:       time.Now().Unix(),
	}); err != nil {
		c.log.Debugf("Failed to record inbound sticker recency for %s: %v", input.ID, err)
		return
	}
	c.publishStickerLibraryChangedDebounced(app.StickerSourceRecent)
}

func (c *Client) clearComposingAfterLiveIncomingMessage(message app.Message) {
	if message.Direction != appstore.DirectionIncoming || message.ChatID == "" || message.SenderID == "" {
		return
	}
	c.daemon.ClearChatComposing(message.ChatID, message.SenderID)
}

func (c *Client) shouldNotifyLiveMessage(message app.Message, chat app.Chat, countUnread bool) bool {
	return countUnread && notificationTimestampFresh(message.TimestampUnix, time.Now()) &&
		c.ShouldNotifyChat(chat.ID) && !chatNotificationsMuted(chat.IsMuted, chat.MuteEndTimestamp)
}

// chatNotificationsMuted reports whether a chat's mute is currently in effect.
// muteEndMillis is unix millis; -1 means muted forever, 0 means not muted.
func chatNotificationsMuted(isMuted bool, muteEndMillis int64) bool {
	return isMuted && (muteEndMillis == -1 || muteEndMillis > time.Now().UnixMilli())
}

func notificationTimestampFresh(timestampUnix int64, now time.Time) bool {
	if timestampUnix <= 0 {
		return true
	}
	return now.Sub(time.Unix(timestampUnix, 0)) <= liveNotificationMaxAge
}

// mediaMessageInput picks the row shape for whatever this message turns out to
// be, then tags it with the album that owns it. History sync calls this
// directly, so anything that belongs to every media kind belongs here rather
// than in the ingest path above it.
func (c *Client) mediaMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	input, ok := c.mediaMessageInputForKind(ctx, evt, opts)
	if !ok {
		return input, false
	}
	applyAlbumAssociation(&input, evt.Message)
	return input, true
}

func (c *Client) mediaMessageInputForKind(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if input, ok := c.imageMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.stickerMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.videoMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.audioMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.documentMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.locationMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.contactMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.pollMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.groupInviteMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.eventMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.albumMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.interactiveMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.commerceMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.stickerPackMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	if input, ok := c.callLogMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	return c.unsupportedMessageInput(ctx, evt, opts)
}

// mediaInputBase builds the half of a media row that has nothing to do with the
// media itself: identity, naming, direction, unread accounting and the reply
// quote. The chat id comes back with it because callers need it for thumbnail
// paths. ok=false means the event is not storable at all.
func (c *Client) mediaInputBase(ctx context.Context, evt *events.Message, opts ingestOptions, text string, contextInfo *waE2E.ContextInfo) (appstore.TextMessageInput, string, bool) {
	info := evt.Info
	chatJID := c.normalizeJIDForChat(ctx, info.Chat)
	chatID := chatJID.String()
	if chatID == "" || info.ID == "" {
		return appstore.TextMessageInput{}, "", false
	}

	direction, status := messageDirectionAndStatus(info, opts)
	return appstore.TextMessageInput{
		ID:             internalMessageIDForChat(chatID, info.ID),
		ChatID:         chatID,
		ChatName:       c.chatName(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
		ChatNameSource: c.chatNameSource(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
		SenderID:       senderID(info),
		SenderName:     c.senderName(ctx, senderJID(info)),
		Text:           text,
		Timestamp:      c.messageTimestamp(info, opts, evt.SourceWebMsg),
		Direction:      direction,
		Status:         status,
		IsGroup:        info.IsGroup,
		CountUnread:    shouldCountUnread(evt, opts),
		ReplyTo:        c.replyFromContextInfo(ctx, chatID, contextInfo),
		// Mentions come from the context info the caller already picked out for
		// this kind, not from re-deriving it: a captioned photo or video can
		// @-mention people, and until now the media path dropped every one.
		Mentions: c.resolveMentions(ctx, mentionedJIDsFromContextInfo(contextInfo)),
		// The sender's client marks forwarded copies in the same context
		// info; without this only our own forwards (flagged at send time)
		// ever rendered the header, so forwarded-to-us rows showed it on
		// the phone but never on the desktop.
		IsForwarded: contextInfo.GetIsForwarded(),
	}, chatID, true
}

// videoMessageInput covers all three video-shaped payloads, which share the
// VideoMessage type on the wire: ordinary videos, GIFs (GifPlayback, rendered
// muted and looping), and round video notes (PtvMessage).
func (c *Client) videoMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.MediaMessageInput{}, false
	}
	// View-once media is meant to be opened on the phone only; it falls
	// through to the unsupported-message tombstone.
	if evt.IsViewOnce {
		return appstore.MediaMessageInput{}, false
	}

	var (
		videoMsg *waE2E.VideoMessage
		kind     string
	)
	switch {
	case evt.Message.GetPtvMessage() != nil:
		videoMsg = evt.Message.GetPtvMessage()
		kind = appstore.MediaKindVideoNote
	case evt.Message.GetVideoMessage() != nil:
		videoMsg = evt.Message.GetVideoMessage()
		kind = appstore.MediaKindVideo
		if videoMsg.GetGifPlayback() {
			kind = appstore.MediaKindGIF
		}
	default:
		return appstore.MediaMessageInput{}, false
	}

	base, chatID, ok := c.mediaInputBase(ctx, evt, opts, videoMsg.GetCaption(), videoMsg.GetContextInfo())
	if !ok {
		return appstore.MediaMessageInput{}, false
	}

	payload, err := proto.Marshal(videoMsg)
	if err != nil {
		c.log.Warnf("Failed to serialize video metadata for message %s: %v", evt.Info.ID, err)
		return appstore.MediaMessageInput{}, false
	}
	mimeType := videoMsg.GetMimetype()
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	return appstore.MediaMessageInput{
		TextMessageInput:        base,
		MediaKind:               kind,
		MediaMimeType:           mimeType,
		MediaThumbnailLocalPath: c.saveMessageThumbnail(chatID, base.ID, videoMsg.GetJPEGThumbnail()),
		MediaWidth:              int32(videoMsg.GetWidth()),
		MediaHeight:             int32(videoMsg.GetHeight()),
		MediaAnimated:           kind == appstore.MediaKindGIF,
		MediaPayload:            payload,
		MediaDurationSecs:       int32(videoMsg.GetSeconds()),
		MediaSizeBytes:          int64(videoMsg.GetFileLength()),
	}, true
}

// audioMessageInput covers recorded voice notes (PTT, which carry a waveform)
// and shared audio files, which differ only by the PTT flag.
func (c *Client) audioMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.MediaMessageInput{}, false
	}
	if evt.IsViewOnce {
		return appstore.MediaMessageInput{}, false
	}

	audioMsg := evt.Message.GetAudioMessage()
	if audioMsg == nil {
		return appstore.MediaMessageInput{}, false
	}

	kind := appstore.MediaKindAudio
	if audioMsg.GetPTT() {
		kind = appstore.MediaKindVoice
	}

	// AudioMessage has no caption field: the bubble is the waveform alone.
	base, _, ok := c.mediaInputBase(ctx, evt, opts, "", audioMsg.GetContextInfo())
	if !ok {
		return appstore.MediaMessageInput{}, false
	}

	payload, err := proto.Marshal(audioMsg)
	if err != nil {
		c.log.Warnf("Failed to serialize audio metadata for message %s: %v", evt.Info.ID, err)
		return appstore.MediaMessageInput{}, false
	}
	mimeType := audioMsg.GetMimetype()
	if mimeType == "" {
		mimeType = "audio/ogg; codecs=opus"
	}

	return appstore.MediaMessageInput{
		TextMessageInput:  base,
		MediaKind:         kind,
		MediaMimeType:     mimeType,
		MediaPayload:      payload,
		MediaDurationSecs: int32(audioMsg.GetSeconds()),
		MediaSizeBytes:    int64(audioMsg.GetFileLength()),
		MediaWaveform:     normalizedWaveform(audioMsg.GetWaveform()),
	}, true
}

func (c *Client) documentMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.MediaMessageInput{}, false
	}
	if evt.IsViewOnce {
		return appstore.MediaMessageInput{}, false
	}

	// DocumentWithCaptionMessage arrives already unwrapped (whatsmeow calls
	// UnwrapRaw for both live messages and history), so the caption is on the
	// inner DocumentMessage by the time we see it.
	docMsg := evt.Message.GetDocumentMessage()
	if docMsg == nil {
		return appstore.MediaMessageInput{}, false
	}

	base, _, ok := c.mediaInputBase(ctx, evt, opts, docMsg.GetCaption(), docMsg.GetContextInfo())
	if !ok {
		return appstore.MediaMessageInput{}, false
	}

	payload, err := proto.Marshal(docMsg)
	if err != nil {
		c.log.Warnf("Failed to serialize document metadata for message %s: %v", evt.Info.ID, err)
		return appstore.MediaMessageInput{}, false
	}
	mimeType := docMsg.GetMimetype()
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	fileName := strings.TrimSpace(docMsg.GetFileName())
	if fileName == "" {
		fileName = strings.TrimSpace(docMsg.GetTitle())
	}

	return appstore.MediaMessageInput{
		TextMessageInput: base,
		MediaKind:        appstore.MediaKindDocument,
		MediaMimeType:    mimeType,
		MediaPayload:     payload,
		MediaSizeBytes:   int64(docMsg.GetFileLength()),
		MediaFileName:    fileName,
		MediaPageCount:   int32(docMsg.GetPageCount()),
	}, true
}

// waveformBuckets is the number of amplitude samples WhatsApp ships with a
// voice note, and the number the bubble draws.
const waveformBuckets = 64

// normalizedWaveform keeps a wire waveform only when it is the shape the
// renderer expects: 64 bytes of 0-100. Anything else (a sender that omitted it,
// or a client that sized it differently) is dropped so the daemon can derive a
// real one after download instead of drawing garbage.
func normalizedWaveform(waveform []byte) []byte {
	if len(waveform) != waveformBuckets {
		return nil
	}
	out := make([]byte, waveformBuckets)
	for i, v := range waveform {
		if v > 100 {
			v = 100
		}
		out[i] = v
	}
	return out
}

func (c *Client) imageMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.MediaMessageInput{}, false
	}
	// View-once photos are meant to be opened on the phone only; they fall
	// through to the unsupported-message tombstone instead of an image bubble.
	if evt.IsViewOnce {
		return appstore.MediaMessageInput{}, false
	}

	imgMsg := evt.Message.GetImageMessage()
	if imgMsg == nil {
		return appstore.MediaMessageInput{}, false
	}

	info := evt.Info
	chatJID := c.normalizeJIDForChat(ctx, info.Chat)
	chatID := chatJID.String()
	if chatID == "" || info.ID == "" {
		return appstore.MediaMessageInput{}, false
	}

	direction, status := messageDirectionAndStatus(info, opts)

	payload, err := proto.Marshal(imgMsg)
	if err != nil {
		c.log.Warnf("Failed to serialize image metadata for message %s: %v", info.ID, err)
		return appstore.MediaMessageInput{}, false
	}
	mimeType := imgMsg.GetMimetype()
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	thumbnailLocalPath := c.saveMessageThumbnail(chatID, internalMessageIDForChat(chatID, info.ID), imgMsg.GetJPEGThumbnail())

	caption := imgMsg.GetCaption()
	return appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:             internalMessageIDForChat(chatID, info.ID),
			ChatID:         chatID,
			ChatName:       c.chatName(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
			ChatNameSource: c.chatNameSource(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
			SenderID:       senderID(info),
			SenderName:     c.senderName(ctx, senderJID(info)),
			Text:           caption,
			Timestamp:      c.messageTimestamp(info, opts, evt.SourceWebMsg),
			Direction:      direction,
			Status:         status,
			IsGroup:        info.IsGroup,
			CountUnread:    shouldCountUnread(evt, opts),
			ReplyTo:        c.replyFromContextInfo(ctx, chatID, imgMsg.GetContextInfo()),
			IsForwarded:    imgMsg.GetContextInfo().GetIsForwarded(),
		},
		MediaKind:               appstore.MediaKindImage,
		MediaMimeType:           mimeType,
		MediaThumbnailLocalPath: thumbnailLocalPath,
		MediaWidth:              int32(imgMsg.GetWidth()),
		MediaHeight:             int32(imgMsg.GetHeight()),
		MediaPayload:            payload,
	}, true
}

func (c *Client) stickerMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.MediaMessageInput{}, false
	}

	stickerMsg := evt.Message.GetStickerMessage()
	if stickerMsg == nil {
		return appstore.MediaMessageInput{}, false
	}

	info := evt.Info
	chatJID := c.normalizeJIDForChat(ctx, info.Chat)
	chatID := chatJID.String()
	if chatID == "" || info.ID == "" {
		return appstore.MediaMessageInput{}, false
	}

	direction, status := messageDirectionAndStatus(info, opts)
	payload, err := proto.Marshal(stickerMsg)
	if err != nil {
		c.log.Warnf("Failed to serialize sticker metadata for message %s: %v", info.ID, err)
		return appstore.MediaMessageInput{}, false
	}
	mimeType := stickerMsg.GetMimetype()
	if mimeType == "" {
		mimeType = "image/webp"
	}
	thumbnailLocalPath := c.saveMessageThumbnailWithExtension(chatID, internalMessageIDForChat(chatID, info.ID), stickerMsg.GetPngThumbnail(), ".thumb.png")

	return appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:             internalMessageIDForChat(chatID, info.ID),
			ChatID:         chatID,
			ChatName:       c.chatName(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
			ChatNameSource: c.chatNameSource(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
			SenderID:       senderID(info),
			SenderName:     c.senderName(ctx, senderJID(info)),
			Timestamp:      c.messageTimestamp(info, opts, evt.SourceWebMsg),
			Direction:      direction,
			Status:         status,
			IsGroup:        info.IsGroup,
			CountUnread:    shouldCountUnread(evt, opts),
			ReplyTo:        c.replyFromContextInfo(ctx, chatID, stickerMsg.GetContextInfo()),
			IsForwarded:    stickerMsg.GetContextInfo().GetIsForwarded(),
		},
		MediaKind:               appstore.MediaKindSticker,
		MediaMimeType:           mimeType,
		MediaThumbnailLocalPath: thumbnailLocalPath,
		MediaWidth:              int32(stickerMsg.GetWidth()),
		MediaHeight:             int32(stickerMsg.GetHeight()),
		MediaAnimated:           stickerMsg.GetIsAnimated(),
		MediaPayload:            payload,
		MediaCacheKey:           stickerMediaCacheKey(stickerMsg),
	}, true
}

// unsupportedMessageInput stores an honest tombstone for real messages whose
// payload whatevr cannot render yet (documents, voice notes, video, polls,
// view-once media, ...). The human-readable label rides in the text column so
// bubbles, chat previews and notifications all pick it up for free. Protocol
// noise (poll votes, app-state keys, ...) carries no row and stays invisible.
//
// A kind this build has never heard of gets a generic tombstone rather than
// nothing: WhatsApp ships new message types faster than whatevr learns them,
// and a grey bubble is a hole somebody can see and report. The payload is kept
// so the row can be upgraded once the kind is understood.
func (c *Client) unsupportedMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.MediaMessageInput{}, false
	}
	label, ok := unsupportedMessageLabel(evt)
	if !ok {
		field, unknown := unrecognizedPayloadField(evt.Message)
		if !unknown {
			return appstore.MediaMessageInput{}, false
		}
		c.log.Warnf("Storing a tombstone for message %s: nothing handles payload %q", evt.Info.ID, field)
		label = "Unsupported message"
	}

	base, _, ok := c.mediaInputBase(ctx, evt, opts, label, unsupportedContextInfo(evt.Message))
	if !ok {
		return appstore.MediaMessageInput{}, false
	}

	input := appstore.MediaMessageInput{
		TextMessageInput: base,
		MediaKind:        appstore.MediaKindUnsupported,
		// Keep the marshalled payload even though nothing reads it today. A
		// tombstone that stored nothing could never be upgraded when a later
		// build learned its kind, which is why every location and poll ingested
		// before this change stays grey forever. This one will not.
		MediaPayload: marshalMessagePayload(evt.Message),
	}
	// Inbound view-once media keeps its keys on the row (real kind + payload)
	// so an explicit `media.save` can fetch it later. It still renders as a
	// tombstone: messageKind() forces inbound view-once rows to the
	// `unsupported` wire kind, so no auto-download policy ever picks them up.
	if kind, mime, payload, ok := viewOnceTombstoneAttrs(evt); ok {
		input.MediaKind = kind
		input.MediaMimeType = mime
		input.MediaPayload = payload
		input.IsViewOnce = true
	}
	return input, true
}

// viewOnceTombstoneAttrs extracts the savable facts of an inbound view-once
// photo/video/voice note: the real media kind plus the wire payload (media
// keys) that a later explicit `media.save` can download with. The row still
// renders as a tombstone — nothing here fetches bytes on its own — so the
// sender's "view on your phone" intent survives until the user deliberately
// overrides it per message.
func viewOnceTombstoneAttrs(evt *events.Message) (kind string, mime string, payload []byte, ok bool) {
	if evt == nil || evt.Message == nil || !evt.IsViewOnce {
		return "", "", nil, false
	}
	msg := evt.Message
	if img := msg.GetImageMessage(); img != nil {
		payload, err := proto.Marshal(img)
		if err != nil {
			return "", "", nil, false
		}
		return appstore.MediaKindImage, defaultMime(img.GetMimetype(), "image/jpeg"), payload, true
	}
	if video := msg.GetVideoMessage(); video != nil {
		payload, err := proto.Marshal(video)
		if err != nil {
			return "", "", nil, false
		}
		return appstore.MediaKindVideo, defaultMime(video.GetMimetype(), "video/mp4"), payload, true
	}
	if audio := msg.GetAudioMessage(); audio != nil {
		payload, err := proto.Marshal(audio)
		if err != nil {
			return "", "", nil, false
		}
		kind := appstore.MediaKindAudio
		if audio.GetPTT() {
			kind = appstore.MediaKindVoice
		}
		return kind, defaultMime(audio.GetMimetype(), "audio/ogg; codecs=opus"), payload, true
	}
	return "", "", nil, false
}

// storeBackfilledMessageSecret persists the message secret of a history-synced
// message into whatsmeow's own secret store. Polls, events, reactions and
// comments are all encrypted against that secret, and whatsmeow only records it
// for messages that arrived live, so without this a poll pulled out of backfill
// decrypts to ErrOriginalMessageSecretNotFound forever.
func (c *Client) storeBackfilledMessageSecret(ctx context.Context, evt *events.Message) {
	if evt == nil || evt.Message == nil {
		return
	}
	secret := evt.Message.GetMessageContextInfo().GetMessageSecret()
	if len(secret) == 0 {
		return
	}
	client := c.currentClient()
	if client == nil || client.Store == nil || client.Store.MsgSecrets == nil {
		return
	}
	if err := client.Store.MsgSecrets.PutMessageSecret(ctx, evt.Info.Chat, evt.Info.Sender, evt.Info.ID, secret); err != nil {
		c.log.Warnf("Failed to store backfilled message secret for %s: %v", evt.Info.ID, err)
	}
}

// marshalMessagePayload serializes a whole waE2E message for later re-parsing.
// A failure is not worth losing the row over: the tombstone still has its label.
func marshalMessagePayload(message *waE2E.Message) []byte {
	if message == nil {
		return nil
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return nil
	}
	return payload
}

// maxMessageUnwrapDepth caps the peel below. Nothing legitimate nests this
// deep, and an unbounded loop on attacker-shaped input is not worth the risk.
const maxMessageUnwrapDepth = 8

// unwrapNestedMessage peels the wrappers whatsmeow's own UnwrapRaw leaves
// alone. Every one of these holds an ordinary message, so leaving it wrapped
// means the payload matches no builder, writes no row, and logs nothing: the
// message simply is not there. Wrappers that are not ordinary messages (status,
// newsletter, bot and settings families) are deliberately left for the
// tombstone, which at least makes them visible.
//
// associatedChildMessage is deliberately not among them. It is not a message
// that arrived wrapped, it is a companion of one that arrived separately: an
// HD photo crosses as the ordinary image and then again, as its own stanza,
// carrying the full-size copy in this wrapper. Peeling it made the companion a
// message of its own, so every HD photo drew twice, at two resolutions, seconds
// apart. See silentMessageFields.
func unwrapNestedMessage(msg *waE2E.Message) *waE2E.Message {
	for range maxMessageUnwrapDepth {
		var inner *waE2E.Message
		switch {
		case msg.GetGroupMentionedMessage().GetMessage() != nil:
			inner = msg.GetGroupMentionedMessage().GetMessage()
		case msg.GetSpoilerMessage().GetMessage() != nil:
			inner = msg.GetSpoilerMessage().GetMessage()
		case msg.GetPollCreationMessageV4().GetMessage() != nil:
			inner = msg.GetPollCreationMessageV4().GetMessage()
		case msg.GetPollCreationOptionImageMessage().GetMessage() != nil:
			inner = msg.GetPollCreationOptionImageMessage().GetMessage()
		case msg.GetAudioStickerMessage().GetMessage() != nil:
			inner = msg.GetAudioStickerMessage().GetMessage()
		case msg.GetBotForwardedMessage().GetMessage() != nil:
			inner = msg.GetBotForwardedMessage().GetMessage()
		}
		if inner == nil {
			return msg
		}
		// The secret and the reply context ride on the wrapper, and whatsmeow
		// carries them inward the same way for the wrappers it peels.
		if inner.MessageContextInfo == nil && msg.MessageContextInfo != nil {
			inner.MessageContextInfo = msg.MessageContextInfo
		}
		msg = inner
	}
	return msg
}

// unsupportedMessageLabel maps not-yet-rendered payload types to a short
// description. ok=false means the message carries nothing user-visible and
// must stay invisible (protocol messages, poll votes, reactions, ...).
//
// This runs last, after every real builder, so anything named here is a kind
// whatevr cannot draw yet. A label is the difference between an honest grey row
// and a hole in the transcript, and the payload is kept with it so a later
// build can upgrade the row in place.
// silentMessageFields are the payload fields that never become a transcript row:
// session plumbing, and the events that modify an existing message rather than
// being one. Anything else set on a message is something a person sent.
//
// It is a denylist on purpose. The allowlist below it can only ever describe
// the kinds this build already knows, and WhatsApp adds them faster than that;
// listing what is definitely not a message is the only version that stays
// correct as the protocol grows.
var silentMessageFields = map[string]bool{
	"senderKeyDistributionMessage":               true,
	"fastRatchetKeySenderKeyDistributionMessage": true,
	"protocolMessage":                            true,
	"messageContextInfo":                         true,
	"stickerSyncRmrMessage":                      true,
	"placeholderMessage":                         true,
	"groupRootKeyShare":                          true,
	"rootSecretDistributeMessage":                true,
	"botPlatformRegistrationSuccessMessage":      true,
	"acp2SettingMessage":                         true,
	// A companion of a message that arrived on its own stanza, not a message.
	// The HD half of a photo is the one that matters here: dropping it shows
	// the standard-quality image once, which is what every other client shows
	// before you ask for HD, instead of the same photo twice.
	"associatedChildMessage": true,
	// Edits to something that already has a row.
	"reactionMessage":                true,
	"encReactionMessage":             true,
	"pollUpdateMessage":              true,
	"pollAddOptionMessage":           true,
	"pollCreationOptionImageMessage": true,
	"encEventResponseMessage":        true,
	"keepInChatMessage":              true,
	"pinInChatMessage":               true,
	"eventCoverImage":                true,
	"statusLinkPreviewMetadata":      true,
	// Status and newsletter housekeeping, none of which is a chat message.
	"statusAddYours":                      true,
	"statusNotificationMessage":           true,
	"newsletterAdminProfileMessage":       true,
	"newsletterAdminProfileStatusMessage": true,
}

// unrecognizedPayloadField names a set field that is neither plumbing nor
// anything the builders above claimed, which is how a message type nobody has
// written code for is told apart from an empty envelope.
func unrecognizedPayloadField(msg *waE2E.Message) (string, bool) {
	found := ""
	msg.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if silentMessageFields[string(fd.Name())] {
			return true
		}
		found = string(fd.Name())
		return false
	})
	return found, found != ""
}

func unsupportedMessageLabel(evt *events.Message) (string, bool) {
	msg := evt.Message
	if evt.IsViewOnce {
		switch {
		case msg.GetImageMessage() != nil:
			return "View once photo", true
		case msg.GetVideoMessage() != nil:
			return "View once video", true
		case msg.GetAudioMessage() != nil:
			return "View once voice message", true
		}
	}
	switch {
	// The business family used to land here as a bare "Message". It now has its
	// own card, and the only member still on this path is the non-hydrated
	// TemplateMessage, whose contents are a template name to be looked up in a
	// catalogue a linked device is never sent.
	case msg.GetTemplateMessage() != nil:
		return "Message", true
	case msg.GetHighlyStructuredMessage() != nil:
		return "Message", true
	case msg.GetConditionalRevealMessage() != nil:
		return "Message", true
	case msg.GetScheduledCallCreationMessage() != nil:
		return "Scheduled call", true
	case msg.GetScheduledCallEditMessage() != nil:
		return "Scheduled call updated", true
	case msg.GetEventInviteMessage() != nil:
		return "Event invite", true
	case msg.GetCommentMessage() != nil:
		return "Comment", true
	case msg.GetMusicMessage() != nil:
		return "Music", true
	case msg.GetRichResponseMessage() != nil:
		return "Meta AI response", true
	case msg.GetContactsArrayMessage() != nil:
		return "Contacts", true
	case msg.GetRequestPhoneNumberMessage() != nil:
		return "Phone number request", true
	case msg.GetNewsletterAdminInviteMessage() != nil:
		return "Channel admin invite", true
	case msg.GetNewsletterFollowerInviteMessageV2() != nil:
		return "Channel invite", true
	case msg.GetMessageHistoryBundle() != nil:
		return "Chat history", true
	case msg.GetPollResultSnapshotMessage() != nil, msg.GetPollResultSnapshotMessageV3() != nil:
		return "Poll results", true
	case msg.GetInvoiceMessage() != nil:
		return "Invoice", true
	case msg.GetDeclinePaymentRequestMessage() != nil:
		return "Payment request declined", true
	case msg.GetCancelPaymentRequestMessage() != nil:
		return "Payment request cancelled", true
	case msg.GetPaymentReminderMessage() != nil:
		return "Payment reminder", true
	case msg.GetSplitPaymentMessage() != nil:
		return "Split payment", true
	case msg.GetSplitPaymentUpdateMessage() != nil:
		return "Split payment update", true
	}
	return "", false
}

// unsupportedContextInfo pulls reply context out of the payload types the
// tombstone path covers, so quoted replies still show their preview.
func unsupportedContextInfo(msg *waE2E.Message) *waE2E.ContextInfo {
	return contextInfoFromMessage(msg)
}

func stickerMediaCacheKey(sticker *waE2E.StickerMessage) string {
	if sticker == nil {
		return ""
	}
	if hash := sticker.GetFileSHA256(); len(hash) > 0 {
		return hex.EncodeToString(hash)
	}
	if hash := sticker.GetFileEncSHA256(); len(hash) > 0 {
		return hex.EncodeToString(hash)
	}
	return ""
}

func (c *Client) saveMessageThumbnail(chatID, messageID string, thumbnail []byte) string {
	return c.saveMessageThumbnailWithExtension(chatID, messageID, thumbnail, ".thumb.jpg")
}

func (c *Client) saveMessageThumbnailWithExtension(chatID, messageID string, thumbnail []byte, extension string) string {
	if len(thumbnail) == 0 {
		return ""
	}
	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", chatID)
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		c.log.Warnf("Failed to create thumbnail cache directory for message %s: %v", messageID, err)
		return ""
	}
	localPath := filepath.Join(mediaDir, safeMediaFileName(messageID, extension))
	if err := writeFileAtomic(localPath, thumbnail, 0o600); err != nil {
		c.log.Warnf("Failed to cache thumbnail for message %s: %v", messageID, err)
		return ""
	}
	return localPath
}

func (c *Client) normalizeJIDForChat(ctx context.Context, jid types.JID) types.JID {
	if jid.Server != types.HiddenUserServer {
		return jid
	}

	client := c.currentClient()
	if client == nil || client.Store.LIDs == nil {
		return jid
	}

	pn, err := client.Store.LIDs.GetPNForLID(ctx, jid)
	if err != nil || pn.IsEmpty() {
		return jid
	}

	return pn
}

func (c *Client) textMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.TextMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.TextMessageInput{}, false
	}

	text := textFromMessage(evt.Message)
	if strings.TrimSpace(text) == "" {
		return appstore.TextMessageInput{}, false
	}

	info := evt.Info
	chatJID := c.normalizeJIDForChat(ctx, info.Chat)
	chatID := chatJID.String()
	if chatID == "" {
		return appstore.TextMessageInput{}, false
	}
	if info.ID == "" {
		return appstore.TextMessageInput{}, false
	}

	direction, status := messageDirectionAndStatus(info, opts)

	input := appstore.TextMessageInput{
		ID:             internalMessageIDForChat(chatID, info.ID),
		ChatID:         chatID,
		ChatName:       c.chatName(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
		ChatNameSource: c.chatNameSource(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
		SenderID:       senderID(info),
		SenderName:     c.senderName(ctx, senderJID(info)),
		Text:           text,
		Timestamp:      c.messageTimestamp(info, opts, evt.SourceWebMsg),
		Direction:      direction,
		Status:         status,
		IsGroup:        info.IsGroup,
		CountUnread:    shouldCountUnread(evt, opts),
		ReplyTo:        c.replyFromContextInfo(ctx, chatID, contextInfoFromMessage(evt.Message)),
		Mentions:       c.mentionsFromMessage(ctx, evt.Message),
		IsForwarded:    contextInfoFromMessage(evt.Message).GetIsForwarded(),
	}

	// A link preview attaches to the row rather than replacing it: the message
	// is still its text, and only the card beside it is new.
	if preview := c.linkPreviewFromMessage(chatID, input.ID, evt.Message); preview != nil {
		encoded, err := appstore.EncodePayload(appstore.MessagePayload{LinkPreview: preview})
		if err != nil {
			c.log.Warnf("Failed to encode link preview for %s: %v", input.ID, err)
		} else {
			input.PayloadJSON = encoded
		}
	}

	return input, true
}

// mentionedJIDsFromMessage pulls the @-mention JID list out of a message's
// context info (ExtendedTextMessage / image / sticker). The matching
// `@<userpart>` tokens already live in the message text; the renderer pairs
// them up. Returns nil when there are no mentions.
func mentionedJIDsFromMessage(message *waE2E.Message) []string {
	return mentionedJIDsFromContextInfo(contextInfoFromMessage(message))
}

// mentionedJIDsFromContextInfo is the same thing for a caller that already
// holds the right context info for its kind.
func mentionedJIDsFromContextInfo(contextInfo *waE2E.ContextInfo) []string {
	if contextInfo == nil {
		return nil
	}
	mentioned := contextInfo.GetMentionedJID()
	if len(mentioned) == 0 {
		return nil
	}
	out := make([]string, 0, len(mentioned))
	for _, jid := range mentioned {
		if trimmed := strings.TrimSpace(jid); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// mentionsFromMessage resolves each @-mention in a message to a {JID, name}
// pair, resolving display names through the same senderName chain used for the
// message author. The JID is kept verbatim so its user-part still matches the
// `@<userpart>` token the renderer looks for.
func (c *Client) mentionsFromMessage(ctx context.Context, message *waE2E.Message) []appstore.MessageMention {
	return c.resolveMentions(ctx, mentionedJIDsFromMessage(message))
}

func (c *Client) resolveMentions(ctx context.Context, jids []string) []appstore.MessageMention {
	if len(jids) == 0 {
		return nil
	}
	mentions := make([]appstore.MessageMention, 0, len(jids))
	for _, raw := range jids {
		jid, err := types.ParseJID(raw)
		if err != nil {
			mentions = append(mentions, appstore.MessageMention{JID: raw})
			continue
		}
		// Drop the leading "~" senderName prepends for unsaved push names: a
		// mention chip reads cleaner as "@Name" than "@~Name".
		name := strings.TrimPrefix(c.senderName(ctx, jid), "~")
		mentions = append(mentions, appstore.MessageMention{JID: jid.String(), DisplayName: name})
	}
	return mentions
}

func contextInfoFromMessage(message *waE2E.Message) *waE2E.ContextInfo {
	if message == nil {
		return nil
	}
	if extended := message.GetExtendedTextMessage(); extended != nil {
		return extended.GetContextInfo()
	}
	if image := message.GetImageMessage(); image != nil {
		return image.GetContextInfo()
	}
	if sticker := message.GetStickerMessage(); sticker != nil {
		return sticker.GetContextInfo()
	}
	if video := message.GetVideoMessage(); video != nil {
		return video.GetContextInfo()
	}
	if ptv := message.GetPtvMessage(); ptv != nil {
		return ptv.GetContextInfo()
	}
	if audio := message.GetAudioMessage(); audio != nil {
		return audio.GetContextInfo()
	}
	if document := message.GetDocumentMessage(); document != nil {
		return document.GetContextInfo()
	}
	if location := message.GetLocationMessage(); location != nil {
		return location.GetContextInfo()
	}
	if live := message.GetLiveLocationMessage(); live != nil {
		return live.GetContextInfo()
	}
	if contact := message.GetContactMessage(); contact != nil {
		return contact.GetContextInfo()
	}
	if contacts := message.GetContactsArrayMessage(); contacts != nil {
		return contacts.GetContextInfo()
	}
	if poll := pollCreationFromMessage(message); poll != nil {
		return poll.GetContextInfo()
	}
	if invite := message.GetGroupInviteMessage(); invite != nil {
		return invite.GetContextInfo()
	}
	if event := message.GetEventMessage(); event != nil {
		return event.GetContextInfo()
	}
	if album := message.GetAlbumMessage(); album != nil {
		return album.GetContextInfo()
	}
	if contextInfo := businessContextInfo(message); contextInfo != nil {
		return contextInfo
	}
	return nil
}

func (c *Client) replyFromContextInfo(ctx context.Context, chatID string, contextInfo *waE2E.ContextInfo) appstore.MessageReply {
	if contextInfo == nil || contextInfo.GetStanzaID() == "" {
		return appstore.MessageReply{}
	}

	replyChatID := chatID
	if remoteJID := strings.TrimSpace(contextInfo.GetRemoteJID()); remoteJID != "" {
		if jid, err := types.ParseJID(remoteJID); err == nil {
			replyChatID = c.normalizeJIDForChat(ctx, jid).String()
		}
	}
	if replyChatID == "" {
		return appstore.MessageReply{}
	}

	senderID, senderName := c.replySenderFromParticipant(ctx, contextInfo.GetParticipant())
	direction := ""
	if senderID == "me" {
		direction = appstore.DirectionOutgoing
	} else if senderID != "" {
		direction = appstore.DirectionIncoming
	}
	text, mediaKind, mediaMimeType := quotedReplyPreview(contextInfo.GetQuotedMessage())

	return appstore.MessageReply{
		MessageID:     internalMessageIDForChat(replyChatID, types.MessageID(contextInfo.GetStanzaID())),
		SenderID:      senderID,
		SenderName:    senderName,
		Text:          text,
		MediaKind:     mediaKind,
		MediaMimeType: mediaMimeType,
		Direction:     direction,
	}
}

func (c *Client) replySenderFromParticipant(ctx context.Context, participant string) (string, string) {
	participant = strings.TrimSpace(participant)
	if participant == "" {
		return "", ""
	}
	jid, err := types.ParseJID(participant)
	if err != nil {
		return participant, ""
	}
	jid = bareAvatarJID(c.normalizeJIDForChat(ctx, jid))
	if c.isOwnJID(jid) {
		return "me", ""
	}
	return jid.String(), c.senderName(ctx, jid)
}

func (c *Client) isOwnJID(jid types.JID) bool {
	client := c.currentClient()
	if client == nil || client.Store.ID == nil || jid.IsEmpty() {
		return false
	}
	own := client.Store.ID.ToNonAD()
	jid = jid.ToNonAD()
	return own.User == jid.User && own.Server == jid.Server
}

func quotedReplyPreview(message *waE2E.Message) (string, string, string) {
	if message == nil {
		return "", "", ""
	}
	if text := textFromMessage(message); text != "" {
		return text, "", ""
	}
	if image := message.GetImageMessage(); image != nil {
		mimeType := image.GetMimetype()
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		return image.GetCaption(), appstore.MediaKindImage, mimeType
	}
	if sticker := message.GetStickerMessage(); sticker != nil {
		mimeType := sticker.GetMimetype()
		if mimeType == "" {
			mimeType = "image/webp"
		}
		return "", appstore.MediaKindSticker, mimeType
	}
	if ptv := message.GetPtvMessage(); ptv != nil {
		return "", appstore.MediaKindVideoNote, defaultMime(ptv.GetMimetype(), "video/mp4")
	}
	if video := message.GetVideoMessage(); video != nil {
		kind := appstore.MediaKindVideo
		if video.GetGifPlayback() {
			kind = appstore.MediaKindGIF
		}
		return video.GetCaption(), kind, defaultMime(video.GetMimetype(), "video/mp4")
	}
	if audio := message.GetAudioMessage(); audio != nil {
		kind := appstore.MediaKindAudio
		if audio.GetPTT() {
			kind = appstore.MediaKindVoice
		}
		return "", kind, defaultMime(audio.GetMimetype(), "audio/ogg; codecs=opus")
	}
	if document := message.GetDocumentMessage(); document != nil {
		// A quoted document with no caption shows its filename, matching the
		// chat-list preview.
		text := document.GetCaption()
		if strings.TrimSpace(text) == "" {
			text = document.GetFileName()
		}
		return text, appstore.MediaKindDocument, defaultMime(document.GetMimetype(), "application/octet-stream")
	}
	if location := message.GetLocationMessage(); location != nil {
		payload := locationPayloadFromMessage(location)
		kind := appstore.MediaKindLocation
		if payload.Live {
			kind = appstore.MediaKindLiveLocation
		}
		// A quoted location shows where it is, not the word "Location": that is
		// the whole content of the message.
		return locationSummary(payload), kind, "image/png"
	}
	if live := message.GetLiveLocationMessage(); live != nil {
		return formatCoordinates(live.GetDegreesLatitude(), live.GetDegreesLongitude()),
			appstore.MediaKindLiveLocation, "image/png"
	}
	if contact := message.GetContactMessage(); contact != nil {
		return cardFromContactMessage(contact).DisplayName, appstore.MediaKindContact, ""
	}
	if contacts := message.GetContactsArrayMessage(); contacts != nil {
		return strings.TrimSpace(contacts.GetDisplayName()), appstore.MediaKindContacts, ""
	}
	if poll := pollCreationFromMessage(message); poll != nil {
		return strings.TrimSpace(poll.GetName()), appstore.MediaKindPoll, ""
	}
	if invite := message.GetGroupInviteMessage(); invite != nil {
		return groupInviteSummary(invite), appstore.MediaKindGroupInvite, ""
	}
	if event := message.GetEventMessage(); event != nil {
		return eventSummary(event), appstore.MediaKindEvent, ""
	}
	if album := message.GetAlbumMessage(); album != nil {
		return albumSummary(&appstore.AlbumPayload{
			ExpectedImages: int(album.GetExpectedImageCount()),
			ExpectedVideos: int(album.GetExpectedVideoCount()),
		}), appstore.MediaKindAlbum, ""
	}
	if payload, _, ok := interactivePayloadFromMessage(message); ok && payload.HasContent() {
		return interactiveSummary(payload), appstore.MediaKindInteractive, ""
	}
	if payload, _, _, ok := commercePayloadFromMessage(message); ok {
		return commerceSummary(payload), commerceMediaKind(payload.Kind), ""
	}
	if pack := message.GetStickerPackMessage(); pack != nil {
		return strings.TrimSpace(pack.GetName()), appstore.MediaKindStickerPack, ""
	}
	if log := message.GetCallLogMesssage(); log != nil {
		// A quote of a call log cannot know which side it was on, so it takes
		// the incoming reading: "Missed voice call" is the one somebody would
		// be quoting.
		return callLogSummary(&appstore.CallLogPayload{
			Video:        log.GetIsVideo(),
			Outcome:      callOutcomeName(log.GetCallOutcome()),
			DurationSecs: log.GetDurationSecs(),
			Participants: len(log.GetParticipants()),
		}, false), appstore.MediaKindCallLog, ""
	}
	return "", "", ""
}

func defaultMime(mimeType, fallback string) string {
	if mimeType == "" {
		return fallback
	}
	return mimeType
}

// maybeUpdateStatusFromHistory applies the status reported by history sync.
// History sync is the same source official WhatsApp clients use for their
// stored message state, so it may correct a prior optimistic receipt mapping.
func (c *Client) maybeUpdateStatusFromHistory(ctx context.Context, internalID, status string) {
	message, changed, err := c.store.UpdateMessageStatusFromHistory(ctx, internalID, status)
	if err != nil {
		c.log.Warnf("Failed to apply history-sync status for %s: %v", internalID, err)
		return
	}
	if !changed {
		return
	}
	c.publishMessageStatusUpdated(ctx, message)
}

func messageDirectionAndStatus(info types.MessageInfo, opts ingestOptions) (string, string) {
	if info.IsFromMe {
		status := appstore.StatusSent
		if opts.source == sourceHistorySync && opts.historyStatus != "" {
			status = opts.historyStatus
		}
		return appstore.DirectionOutgoing, status
	}
	return appstore.DirectionIncoming, appstore.StatusDelivered
}

// futureTimestampSlack is how far ahead a message may legitimately claim to be.
// Clocks disagree by seconds, not days. Anything beyond this is a broken sender
// clock, and left alone it pins the message to the top of the chat forever,
// because nothing that arrives later can ever sort above it.
const futureTimestampSlack = 12 * time.Hour

func (c *Client) messageTimestamp(info types.MessageInfo, opts ingestOptions, webMsg *waWeb.WebMessageInfo) time.Time {
	stated := clampFutureTimestamp(rawMessageTimestamp(info, opts, webMsg), time.Now())
	return c.liveOrderedTimestamp(info, opts, stated)
}

func rawMessageTimestamp(info types.MessageInfo, opts ingestOptions, webMsg *waWeb.WebMessageInfo) time.Time {
	if !opts.timestampOverride.IsZero() {
		return opts.timestampOverride
	}
	if opts.source == sourceHistorySync && info.IsFromMe && webMsg != nil {
		if timestamp, ok := whatsAppUnixTimestamp(webMsg.GetMessageC2STimestamp()); ok {
			return timestamp
		}
	}
	return info.Timestamp
}

func clampFutureTimestamp(timestamp, now time.Time) time.Time {
	if timestamp.IsZero() || !timestamp.After(now.Add(futureTimestampSlack)) {
		return timestamp
	}
	return now
}

func whatsAppUnixTimestamp(value uint64) (time.Time, bool) {
	const maxReasonableUnixSeconds = 4102444800 // 2100-01-01
	if value == 0 {
		return time.Time{}, false
	}
	if value <= maxReasonableUnixSeconds {
		return time.Unix(int64(value), 0), true
	}
	if value <= maxReasonableUnixSeconds*1000 {
		return time.UnixMilli(int64(value)), true
	}
	return time.Time{}, false
}

func historySyncProgressPhase(complete bool) app.HistorySyncPhase {
	if complete {
		return app.HistorySyncPhaseComplete
	}
	return app.HistorySyncPhaseProcessing
}

func shouldCountUnread(evt *events.Message, opts ingestOptions) bool {
	if evt == nil || evt.Info.IsFromMe {
		return false
	}
	if opts.source == sourceHistorySync {
		// History-sync rows never bump the unread count; the chat-row
		// unread is set authoritatively from conv.UnreadCount once the
		// conversation has been processed.
		return false
	}
	if opts.forceRead {
		return false
	}
	// Whatsmeow internally re-emits messages it parses out of a sync
	// blob with SourceWebMsg set; only freshly-streamed events leave it
	// nil. Use that to skip double-counting.
	return evt.SourceWebMsg == nil
}

func textFromMessage(message *waE2E.Message) string {
	if text := message.GetConversation(); text != "" {
		return text
	}
	if text := message.GetExtendedTextMessage().GetText(); text != "" {
		return text
	}
	// Pressing a button on a business message sends back a message whose whole
	// content is the words that were on the button, which is exactly what
	// WhatsApp shows for one. So it is text, and every path that handles text
	// (search, quoting, copying, the chat-list preview) handles it for free.
	if text := interactiveResponseText(message); text != "" {
		return text
	}
	return ""
}

func senderID(info types.MessageInfo) string {
	if info.IsFromMe {
		return "me"
	}
	if !info.Sender.IsEmpty() {
		return bareAvatarJID(info.Sender).String()
	}
	return bareAvatarJID(info.Chat).String()
}

func senderJID(info types.MessageInfo) types.JID {
	if info.IsFromMe {
		return types.JID{}
	}
	if !info.Sender.IsEmpty() {
		return bareAvatarJID(info.Sender)
	}
	return bareAvatarJID(info.Chat)
}

func historySyncChatName(conv *waHistorySync.Conversation) string {
	for _, name := range []string{conv.GetDisplayName(), conv.GetName()} {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// mapWebMessageStatus translates the WhatsApp WebMessageInfo.Status field
// into our internal status string, used for own-message rows during history
// sync. Returns "" when the field is absent.
func mapWebMessageStatus(webMsg *waWeb.WebMessageInfo) string {
	if webMsg == nil || webMsg.Status == nil {
		return ""
	}
	switch webMsg.GetStatus() {
	case waWeb.WebMessageInfo_PENDING:
		return appstore.StatusPending
	case waWeb.WebMessageInfo_SERVER_ACK:
		return appstore.StatusSent
	case waWeb.WebMessageInfo_DELIVERY_ACK:
		return appstore.StatusDelivered
	case waWeb.WebMessageInfo_READ, waWeb.WebMessageInfo_PLAYED:
		return appstore.StatusRead
	case waWeb.WebMessageInfo_ERROR:
		return appstore.StatusFailed
	default:
		return ""
	}
}

func historySyncType(t waHistorySync.HistorySync_HistorySyncType) app.HistorySyncType {
	switch t {
	case waHistorySync.HistorySync_INITIAL_BOOTSTRAP:
		return app.HistorySyncTypeInitialBootstrap
	case waHistorySync.HistorySync_INITIAL_STATUS_V3:
		return app.HistorySyncTypeInitialStatusV3
	case waHistorySync.HistorySync_FULL:
		return app.HistorySyncTypeFull
	case waHistorySync.HistorySync_RECENT:
		return app.HistorySyncTypeRecent
	case waHistorySync.HistorySync_PUSH_NAME:
		return app.HistorySyncTypePushName
	case waHistorySync.HistorySync_NON_BLOCKING_DATA:
		return app.HistorySyncTypeNonBlockingData
	case waHistorySync.HistorySync_ON_DEMAND:
		return app.HistorySyncTypeOnDemand
	default:
		return app.HistorySyncTypeUnspecified
	}
}

// historySyncIsComplete reports whether the chunk we just processed should
// dismiss the "Syncing chat history…" indicator. PUSH_NAME / NON_BLOCKING_DATA
// don't carry progress so we always treat them as complete; otherwise we wait
// for the server to report 100%.
func historySyncIsComplete(syncType app.HistorySyncType, progress uint32) bool {
	switch syncType {
	case app.HistorySyncTypePushName, app.HistorySyncTypeNonBlockingData:
		return true
	}
	return progress >= 100
}

func (c *Client) chatName(ctx context.Context, chatJID types.JID, isGroup bool, override, overrideSource string) string {
	name, _ := c.displayNameForChat(ctx, chatJID, isGroup, override, overrideSource)
	return name
}

func (c *Client) chatNameSource(ctx context.Context, chatJID types.JID, isGroup bool, override, overrideSource string) string {
	_, source := c.displayNameForChat(ctx, chatJID, isGroup, override, overrideSource)
	return source
}

func (c *Client) displayNameForChat(ctx context.Context, chatJID types.JID, isGroup bool, override, overrideSource string) (string, string) {
	if override = strings.TrimSpace(override); override != "" {
		return override, overrideSource
	}
	if isGroup {
		return "", ""
	}
	if name := c.contactNameForJID(ctx, chatJID); name != "" {
		return name, appstore.ChatNameSourceContact
	}
	if phone := formatPhoneDisplayName(chatJID); phone != "" {
		return phone, appstore.ChatNameSourcePhone
	}
	if name := c.whatsAppNameForJID(ctx, chatJID); name != "" {
		return name, appstore.ChatNameSourceWhatsApp
	}
	return "", ""
}

func (c *Client) updateChatNamesFromHistorySync(ctx context.Context, evt *events.HistorySync) {
	if evt == nil || evt.Data == nil {
		return
	}
	client := c.currentClient()

	for _, contact := range evt.Data.GetInlineContacts() {
		name := firstNonEmpty(contact.GetFullName(), contact.GetFirstName(), contact.GetUsername())
		if name == "" {
			continue
		}
		for _, rawJID := range []string{contact.GetPnJID(), contact.GetLidJID()} {
			if rawJID == "" {
				continue
			}
			jid, err := types.ParseJID(rawJID)
			if err != nil {
				continue
			}
			if client != nil && client.Store.Contacts != nil {
				if err := client.Store.Contacts.PutContactName(ctx, jid, contact.GetFirstName(), contact.GetFullName()); err != nil {
					c.log.Warnf("Failed to store contact name for %s: %v", jid, err)
				}
			}
			if err := c.store.UpdateSenderName(ctx, jid.String(), name); err != nil {
				c.log.Warnf("Failed to store sender name for %s: %v", jid, err)
			}
			c.updateChatName(ctx, jid.String(), name, appstore.ChatNameSourceContact)
		}
	}

	refreshChatIDs := make(map[string]struct{})
	for _, push := range evt.Data.GetPushnames() {
		name := strings.TrimSpace(push.GetPushname())
		if name == "" || name == "-" {
			continue
		}
		jid, err := types.ParseJID(push.GetID())
		if err != nil {
			continue
		}
		if client != nil && client.Store.Contacts != nil {
			if _, _, err := client.Store.Contacts.PutPushName(ctx, jid, name); err != nil {
				c.log.Warnf("Failed to store push name for %s: %v", jid, err)
			}
		}
		if err := c.store.UpdateSenderName(ctx, jid.String(), whatsAppDisplayName(name)); err != nil {
			c.log.Warnf("Failed to store sender push name for %s: %v", jid, err)
		}
		chatIDs, err := c.store.ListChatIDsBySenderID(ctx, jid.String())
		if err != nil {
			c.log.Warnf("Failed to list chats for sender push name refresh %s: %v", jid, err)
			continue
		}
		for _, chatID := range chatIDs {
			if chatID != "" && chatID != jid.String() {
				refreshChatIDs[chatID] = struct{}{}
			}
		}
	}
	for chatID := range refreshChatIDs {
		c.daemon.PublishHistoryBackfilled(chatID, 0)
	}
}

func (c *Client) contactNameForJID(ctx context.Context, jid types.JID) string {
	client := c.currentClient()
	if client == nil || client.Store.Contacts == nil || jid.IsEmpty() {
		return ""
	}

	contact, err := client.Store.Contacts.GetContact(ctx, jid.ToNonAD())
	if err != nil {
		return ""
	}
	return firstNonEmpty(contact.FullName, contact.FirstName)
}

func (c *Client) whatsAppNameForJID(ctx context.Context, jid types.JID) string {
	client := c.currentClient()
	if client == nil || client.Store.Contacts == nil || jid.IsEmpty() {
		return ""
	}

	contact, err := client.Store.Contacts.GetContact(ctx, jid.ToNonAD())
	if err != nil {
		return ""
	}
	return whatsAppDisplayName(firstNonEmpty(contact.PushName, contact.BusinessName))
}

func (c *Client) senderName(ctx context.Context, jid types.JID) string {
	if jid.IsEmpty() {
		return ""
	}
	if name := c.contactNameForJID(ctx, jid); name != "" {
		return name
	}
	// LID JIDs can't be looked up by phone display or contact store directly;
	// resolve to PN first, then retry.
	if jid.Server == types.HiddenUserServer {
		pn := c.normalizeJIDForChat(ctx, jid)
		if !pn.IsEmpty() && pn.String() != jid.String() {
			if name := c.contactNameForJID(ctx, pn); name != "" {
				return name
			}
			if name := c.whatsAppNameForJID(ctx, pn); name != "" {
				return name
			}
			if phone := formatPhoneDisplayName(pn); phone != "" {
				return phone
			}
		}
		if name := c.whatsAppNameForJID(ctx, jid); name != "" {
			return name
		}
		return ""
	}
	if name := c.whatsAppNameForJID(ctx, jid); name != "" {
		return name
	}
	if phone := formatPhoneDisplayName(jid); phone != "" {
		return phone
	}
	return jid.User
}

func whatsAppDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "~") {
		return name
	}
	return "~" + name
}

func (c *Client) updateChatName(ctx context.Context, chatID, name, source string) {
	chat, changed, err := c.store.UpdateChatNameWithSource(ctx, chatID, name, source)
	if err != nil {
		c.log.Warnf("Failed to update chat name for %s: %v", chatID, err)
		return
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
}

func formatPhoneDisplayName(jid types.JID) string {
	if jid.Server != types.DefaultUserServer || jid.User == "" {
		return ""
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, jid.User)
	if digits == "" {
		return ""
	}
	number, err := phonenumbers.Parse("+"+digits, "ZZ")
	if err != nil || !phonenumbers.IsValidNumber(number) {
		return "+" + digits
	}
	return phonenumbers.Format(number, phonenumbers.INTERNATIONAL)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func toDaemonMessage(message appstore.Message) app.Message {
	return app.Message{
		ID:                      message.ID,
		ChatID:                  message.ChatID,
		SenderID:                message.SenderID,
		SenderName:              message.SenderName,
		SenderAvatarLocalPath:   message.SenderAvatarLocalPath,
		Text:                    message.Text,
		TimestampUnix:           message.TimestampUnix,
		SortMS:                  message.SortMS,
		Direction:               message.Direction,
		Status:                  message.Status,
		MediaKind:               message.MediaKind,
		MediaMimeType:           message.MediaMimeType,
		MediaLocalPath:          message.MediaLocalPath,
		MediaThumbnailLocalPath: message.MediaThumbnailLocalPath,
		MediaWidth:              message.MediaWidth,
		MediaHeight:             message.MediaHeight,
		MediaAnimated:           message.MediaAnimated,
		MediaCacheKey:           message.MediaCacheKey,
		Preview:                 appstore.MessagePreviewLine(message),
		IsRevoked:               message.IsRevoked,
		IsEdited:                message.IsEdited,
		IsStarred:               message.IsStarred,
		PinnedUntilUnix:         message.PinnedUntil,
		ReplyTo: app.MessageReply{
			MessageID:     message.ReplyTo.MessageID,
			SenderID:      message.ReplyTo.SenderID,
			SenderName:    message.ReplyTo.SenderName,
			Text:          message.ReplyTo.Text,
			MediaKind:     message.ReplyTo.MediaKind,
			MediaMimeType: message.ReplyTo.MediaMimeType,
			Direction:     message.ReplyTo.Direction,
		},
		Reactions: toDaemonReactions(message.Reactions),
		Mentions:  toDaemonMentions(message.Mentions),
	}
}

func toDaemonMentions(mentions []appstore.MessageMention) []app.Mention {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]app.Mention, len(mentions))
	for i, mention := range mentions {
		out[i] = app.Mention{JID: mention.JID, DisplayName: mention.DisplayName}
	}
	return out
}

func toDaemonReactions(reactions []appstore.Reaction) []app.Reaction {
	if len(reactions) == 0 {
		return nil
	}
	out := make([]app.Reaction, len(reactions))
	for i, reaction := range reactions {
		out[i] = app.Reaction{
			Emoji:         reaction.Emoji,
			SenderID:      reaction.SenderID,
			SenderName:    reaction.SenderName,
			TimestampUnix: reaction.TimestampUnix,
			FromMe:        reaction.FromMe,
		}
	}
	return out
}

func toDaemonChat(chat appstore.Chat) app.Chat {
	return app.Chat{
		ID:                   chat.ID,
		Name:                 chat.Name,
		LastMessage:          chat.LastMessage,
		LastMessageTime:      chat.LastMessageTime,
		LastMessageDirection: chat.LastMessageDirection,
		LastMessageStatus:    chat.LastMessageStatus,
		UnreadCount:          chat.UnreadCount,
		IsGroup:              chat.IsGroup,
		IsPinned:             chat.IsPinned,
		PinnedOrder:          chat.PinnedOrder,
		IsFavorite:           chat.IsFavorite,
		IsArchived:           chat.IsArchived,
		IsMuted:              chat.IsMuted,
		MuteEndTimestamp:     chat.MuteEndTimestamp,
		HistoryExhausted:     chat.HistoryExhausted,
		UpdatedAtUnix:        chat.UpdatedAt,
		AvatarLocalPath:      chat.AvatarLocalPath,
	}
}
