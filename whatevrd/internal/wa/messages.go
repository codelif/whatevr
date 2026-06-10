package wa

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"
	"google.golang.org/protobuf/proto"

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

func (c *Client) handleHistorySync(eventGen uint64, evt *events.HistorySync) {
	if !c.isCurrentEventGeneration(eventGen) {
		return
	}
	ctx := c.backgroundContext()
	if ctx.Err() != nil {
		return
	}
	c.processHistorySyncData(ctx, evt.Data)
}

func (c *Client) processHistorySyncData(ctx context.Context, data *waHistorySync.HistorySync) {
	client := c.currentClient()
	if client == nil || data == nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	c.updateChatNamesFromHistorySync(ctx, &events.HistorySync{Data: data})
	c.ingestRecentStickers(ctx, data.GetRecentStickers())

	syncType := historySyncType(data.GetSyncType())
	progressPercent := data.GetProgress()
	chunkOrder := data.GetChunkOrder()
	c.markHistorySyncAvatarDeferralActive(syncType)
	conversations := data.GetConversations()
	totalMessages := uint32(0)
	for _, conv := range conversations {
		totalMessages += uint32(len(conv.GetMessages()))
	}

	c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:             syncType,
		ProgressPercent:      progressPercent,
		ChunkOrder:           chunkOrder,
		ConversationsInChunk: uint32(len(conversations)),
		MessagesInChunk:      totalMessages,
		IsComplete:           false,
		Phase:                app.HistorySyncPhaseProcessing,
	})
	processedConversations := uint32(0)
	processedMessages := uint32(0)
	lastProgressPublish := time.Now()
	publishProcessingProgress := func(force bool) {
		if !force && time.Since(lastProgressPublish) < historySyncProgressInterval {
			return
		}
		lastProgressPublish = time.Now()
		c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
			SyncType:               syncType,
			ProgressPercent:        progressPercent,
			ChunkOrder:             chunkOrder,
			ConversationsInChunk:   uint32(len(conversations)),
			MessagesInChunk:        totalMessages,
			IsComplete:             false,
			Phase:                  app.HistorySyncPhaseProcessing,
			ProcessedConversations: processedConversations,
			ProcessedMessages:      processedMessages,
		})
	}

	for _, conv := range conversations {
		if ctx.Err() != nil {
			return
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
		if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, chatNameOverride, chatNameSource, chatJID.Server == types.GroupServer); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Warnf("Failed to ensure history-sync chat %s: %v", chatID, err)
			continue
		}
		convUnread := conv.GetUnreadCount()
		convMarkedUnread := conv.GetMarkedAsUnread()
		convPinnedOrder := conv.GetPinned()
		convPinned := convPinnedOrder > 0
		forceRead := convUnread == 0 && !convMarkedUnread

		// Phase 1: parse every message and build its storage input. No app-DB
		// writes happen here, so the batched save below can hold the single
		// write transaction without anything else contending for it.
		type historySaveItem struct {
			item          appstore.MessageSaveItem
			id            string
			historyStatus string
			isMedia       bool
		}
		pending := make([]historySaveItem, 0, len(conv.GetMessages()))
		for _, msg := range conv.GetMessages() {
			if ctx.Err() != nil {
				return
			}
			processedMessages++
			webMsg := msg.GetMessage()
			if webMsg == nil {
				publishProcessingProgress(false)
				continue
			}
			parsedEvt, err := client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				c.log.Warnf("Failed to parse history sync message: %v", err)
				publishProcessingProgress(false)
				continue
			}
			opts := ingestOptions{
				source:           sourceHistorySync,
				chatNameOverride: chatNameOverride,
				chatNameSource:   chatNameSource,
				historyStatus:    mapWebMessageStatus(webMsg),
				forceRead:        forceRead,
			}
			if textInput, ok := c.textMessageInput(ctx, parsedEvt, opts); ok {
				input := textInput
				pending = append(pending, historySaveItem{
					item:          appstore.MessageSaveItem{Text: &input},
					id:            input.ID,
					historyStatus: opts.historyStatus,
				})
			} else if mediaInput, ok := c.mediaMessageInput(ctx, parsedEvt, opts); ok {
				input := mediaInput
				pending = append(pending, historySaveItem{
					item:          appstore.MessageSaveItem{Media: &input},
					id:            input.ID,
					historyStatus: opts.historyStatus,
					isMedia:       true,
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
				return
			}
			c.log.Errorf("Failed to store history sync conversation %s: %v", chatID, err)
			continue
		}

		// Phase 3: post-save work that must stay outside the batch
		// transaction (status corrections and sticker cache resolution do
		// their own store writes).
		for i, saved := range savedBatch {
			if ctx.Err() != nil {
				return
			}
			entry := pending[i]
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
			return
		}
		updatedChat, _, err := c.store.OverwriteChatUnreadCount(ctx, chatID, convUnread)
		if err != nil {
			c.log.Warnf("Failed to overwrite unread count for %s: %v", chatID, err)
		} else if updatedChat.ID != "" {
			lastSavedChat = updatedChat
		}
		updatedChat, pinChanged, err := c.store.UpdateChatPinState(ctx, chatID, convPinned, convPinnedOrder)
		if err != nil {
			c.log.Warnf("Failed to update pinned state for %s: %v", chatID, err)
		} else if updatedChat.ID != "" {
			lastSavedChat = updatedChat
		}

		if messagesAdded > 0 || pinChanged {
			if lastSavedChat.ID != "" {
				c.daemon.PublishChatUpdated(toDaemonChat(lastSavedChat))
			}
			c.daemon.PublishHistoryBackfilled(chatID, messagesAdded)
		}
	}

	if ctx.Err() != nil {
		return
	}

	complete := historySyncIsComplete(syncType, progressPercent)
	c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:               syncType,
		ProgressPercent:        progressPercent,
		ChunkOrder:             chunkOrder,
		ConversationsInChunk:   uint32(len(conversations)),
		MessagesInChunk:        totalMessages,
		IsComplete:             complete,
		Phase:                  historySyncProgressPhase(complete),
		ProcessedConversations: processedConversations,
		ProcessedMessages:      processedMessages,
	})
	if complete {
		c.finishHistorySyncAvatarDeferral(syncType, progressPercent)
	}
}

func (c *Client) handleMessage(ctx context.Context, evt *events.Message, offlineSync bool) {
	if c.handleManualHistorySyncNotification(ctx, evt) {
		return
	}
	source := sourceLive
	if offlineSync {
		source = sourceOfflineSync
	}
	opts := ingestOptions{source: source}
	if timestamp, ok := c.originalRetryTimestamp(ctx, evt); ok {
		opts.timestampOverride = timestamp
	}
	saved, inserted := c.ingestMessage(ctx, evt, opts)
	if offlineSync {
		c.recordOfflineSyncMessage(saved.Chat.ID, inserted)
		return
	}
	c.refreshRawGroupNameForChat(ctx, saved.Chat)
	c.refreshLiveMessageAvatars(ctx, evt)
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
	if evt == nil || c.historySyncBlocksAvatarFetch() {
		return
	}
	chatJID := c.normalizeJIDForChat(ctx, evt.Info.Chat)
	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: bareAvatarJID(chatJID).String()})
	if evt.Info.IsGroup && !evt.Info.Sender.IsEmpty() {
		c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectSender, ID: bareAvatarJID(evt.Info.Sender).String()})
	}
}

// ingestMessage stores a parsed whatsmeow message in the local store and,
// for live messages, publishes the daemon NewMessage event and triggers a
// desktop notification when appropriate.
//
// History-sync messages are saved silently: no NewMessage broadcast, no
// notification. The caller is responsible for emitting per-chat backfill
// events once the conversation has been processed.
func (c *Client) ingestMessage(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.SavedTextMessage, bool) {
	if textInput, ok := c.textMessageInput(ctx, evt, opts); ok {
		saved, err := c.store.SaveTextMessage(ctx, textInput)
		if err != nil {
			c.log.Errorf("Failed to store text message %s: %v", textInput.ID, err)
			return appstore.SavedTextMessage{}, false
		}
		if !saved.Inserted {
			if opts.source == sourceHistorySync && opts.historyStatus != "" {
				c.maybeUpdateStatusFromHistory(ctx, textInput.ID, opts.historyStatus)
			}
			if opts.chatNameOverride != "" && opts.source == sourceLive {
				c.daemon.PublishChatUpdated(toDaemonChat(saved.Chat))
			}
			return saved, false
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
			if c.notifier != nil && c.shouldNotifyLiveMessage(message, chat.ID, textInput.CountUnread) {
				c.notifier.NotifyMessage(ctx, message, chat)
			}
		}
		return saved, true
	}

	if mediaInput, ok := c.mediaMessageInput(ctx, evt, opts); ok {
		saved, err := c.store.SaveMediaMessage(ctx, mediaInput)
		if err != nil {
			c.log.Errorf("Failed to store media message %s: %v", mediaInput.ID, err)
			return appstore.SavedTextMessage{}, false
		}
		if !saved.Inserted {
			if opts.source == sourceHistorySync && opts.historyStatus != "" {
				c.maybeUpdateStatusFromHistory(ctx, mediaInput.ID, opts.historyStatus)
			}
			if opts.chatNameOverride != "" && opts.source == sourceLive {
				c.daemon.PublishChatUpdated(toDaemonChat(saved.Chat))
			}
			return saved, false
		}
		if updated, ok, err := c.resolveCachedStickerMedia(ctx, saved.Message); err != nil {
			c.log.Warnf("Failed to resolve cached sticker media for %s: %v", saved.Message.ID, err)
		} else if ok {
			saved.Message = updated
		}
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
			c.daemon.PublishNewMessage(message, chat)
			c.clearComposingAfterLiveIncomingMessage(message)
			if c.notifier != nil && c.shouldNotifyLiveMessage(message, chat.ID, mediaInput.CountUnread) {
				c.notifier.NotifyMessage(ctx, message, chat)
			}
		}
		return saved, true
	}

	return appstore.SavedTextMessage{}, false
}

func (c *Client) clearComposingAfterLiveIncomingMessage(message app.Message) {
	if message.Direction != appstore.DirectionIncoming || message.ChatID == "" || message.SenderID == "" {
		return
	}
	c.daemon.ClearChatComposing(message.ChatID, message.SenderID)
}

func (c *Client) shouldNotifyLiveMessage(message app.Message, chatID string, countUnread bool) bool {
	return countUnread && notificationTimestampFresh(message.TimestampUnix, time.Now()) && c.ShouldNotifyChat(chatID)
}

func notificationTimestampFresh(timestampUnix int64, now time.Time) bool {
	if timestampUnix <= 0 {
		return true
	}
	return now.Sub(time.Unix(timestampUnix, 0)) <= liveNotificationMaxAge
}

func (c *Client) mediaMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if input, ok := c.imageMessageInput(ctx, evt, opts); ok {
		return input, true
	}
	return c.stickerMessageInput(ctx, evt, opts)
}

func (c *Client) imageMessageInput(ctx context.Context, evt *events.Message, opts ingestOptions) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
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
			Timestamp:      messageTimestamp(info, opts, evt.SourceWebMsg),
			Direction:      direction,
			Status:         status,
			IsGroup:        info.IsGroup,
			CountUnread:    shouldCountUnread(evt, opts),
			ReplyTo:        c.replyFromContextInfo(ctx, chatID, imgMsg.GetContextInfo()),
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
			Timestamp:      messageTimestamp(info, opts, evt.SourceWebMsg),
			Direction:      direction,
			Status:         status,
			IsGroup:        info.IsGroup,
			CountUnread:    shouldCountUnread(evt, opts),
			ReplyTo:        c.replyFromContextInfo(ctx, chatID, stickerMsg.GetContextInfo()),
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

	return appstore.TextMessageInput{
		ID:             internalMessageIDForChat(chatID, info.ID),
		ChatID:         chatID,
		ChatName:       c.chatName(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
		ChatNameSource: c.chatNameSource(ctx, chatJID, info.IsGroup, opts.chatNameOverride, opts.chatNameSource),
		SenderID:       senderID(info),
		SenderName:     c.senderName(ctx, senderJID(info)),
		Text:           text,
		Timestamp:      messageTimestamp(info, opts, evt.SourceWebMsg),
		Direction:      direction,
		Status:         status,
		IsGroup:        info.IsGroup,
		CountUnread:    shouldCountUnread(evt, opts),
		ReplyTo:        c.replyFromContextInfo(ctx, chatID, contextInfoFromMessage(evt.Message)),
	}, true
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
	return "", "", ""
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

func messageTimestamp(info types.MessageInfo, opts ingestOptions, webMsg *waWeb.WebMessageInfo) time.Time {
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
		SortSeq:                 message.SortSeq,
		Direction:               message.Direction,
		Status:                  message.Status,
		MediaKind:               message.MediaKind,
		MediaMimeType:           message.MediaMimeType,
		MediaLocalPath:          message.MediaLocalPath,
		MediaThumbnailLocalPath: message.MediaThumbnailLocalPath,
		MediaWidth:              message.MediaWidth,
		MediaHeight:             message.MediaHeight,
		MediaAnimated:           message.MediaAnimated,
		ReplyTo: app.MessageReply{
			MessageID:     message.ReplyTo.MessageID,
			SenderID:      message.ReplyTo.SenderID,
			SenderName:    message.ReplyTo.SenderName,
			Text:          message.ReplyTo.Text,
			MediaKind:     message.ReplyTo.MediaKind,
			MediaMimeType: message.ReplyTo.MediaMimeType,
			Direction:     message.ReplyTo.Direction,
		},
	}
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
		UpdatedAtUnix:        chat.UpdatedAt,
		AvatarLocalPath:      chat.AvatarLocalPath,
	}
}
