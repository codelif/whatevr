package wa

import (
	"context"
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
)

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
	forceRead bool
}

func (c *Client) handleHistorySync(eventGen uint64, evt *events.HistorySync) {
	if !c.isCurrentEventGeneration(eventGen) {
		return
	}
	client := c.currentClient()
	if client == nil {
		return
	}
	ctx := c.backgroundContext()
	if ctx.Err() != nil {
		return
	}
	c.updateChatNamesFromHistorySync(ctx, evt)

	syncType := historySyncType(evt.Data.GetSyncType())
	progressPercent := evt.Data.GetProgress()
	chunkOrder := evt.Data.GetChunkOrder()
	conversations := evt.Data.GetConversations()
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
	})

	storedAny := false
	for _, conv := range conversations {
		if ctx.Err() != nil || !c.isCurrentEventGeneration(eventGen) {
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
		forceRead := convUnread == 0 && !convMarkedUnread

		messagesAdded := uint32(0)
		var lastSavedChat appstore.Chat
		for _, msg := range conv.GetMessages() {
			if ctx.Err() != nil || !c.isCurrentEventGeneration(eventGen) {
				return
			}
			webMsg := msg.GetMessage()
			if webMsg == nil {
				continue
			}
			parsedEvt, err := client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				c.log.Warnf("Failed to parse history sync message: %v", err)
				continue
			}
			opts := ingestOptions{
				source:           sourceHistorySync,
				chatNameOverride: chatNameOverride,
				chatNameSource:   chatNameSource,
				historyStatus:    mapWebMessageStatus(webMsg),
				forceRead:        forceRead,
			}
			saved, inserted := c.ingestMessage(ctx, parsedEvt, opts)
			if inserted {
				messagesAdded++
				lastSavedChat = saved.Chat
				storedAny = true
			}
		}

		// Mirror the phone's read state on the chat row even if the
		// per-message rows we just inserted didn't bump it (CountUnread
		// is intentionally false during history-sync ingestion).
		if ctx.Err() != nil || !c.isCurrentEventGeneration(eventGen) {
			return
		}
		updatedChat, _, err := c.store.OverwriteChatUnreadCount(ctx, chatID, convUnread)
		if err != nil {
			c.log.Warnf("Failed to overwrite unread count for %s: %v", chatID, err)
		} else if updatedChat.ID != "" {
			lastSavedChat = updatedChat
		}

		if messagesAdded > 0 && c.isCurrentEventGeneration(eventGen) {
			if lastSavedChat.ID != "" {
				c.daemon.PublishChatUpdated(toDaemonChat(lastSavedChat))
			}
			c.daemon.PublishHistoryBackfilled(chatID, messagesAdded)
		}
	}

	if storedAny && c.isCurrentEventGeneration(eventGen) {
		c.scheduleAvatarRefresh(ctx, 2*time.Second)
	}
	if ctx.Err() != nil || !c.isCurrentEventGeneration(eventGen) {
		return
	}

	c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:             syncType,
		ProgressPercent:      progressPercent,
		ChunkOrder:           chunkOrder,
		ConversationsInChunk: uint32(len(conversations)),
		MessagesInChunk:      totalMessages,
		IsComplete:           historySyncIsComplete(syncType, progressPercent),
	})
}

func (c *Client) handleMessage(ctx context.Context, evt *events.Message) {
	if saved, inserted := c.ingestMessage(ctx, evt, ingestOptions{source: sourceLive}); inserted {
		c.scheduleAvatarRefreshForChat(ctx, saved.Chat, 2*time.Second)
		c.scheduleAvatarRefresh(ctx, 2*time.Second)
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
		c.log.Infof("Stored text message %s from %s", saved.Message.ID, saved.Message.SenderID)
		if opts.source == sourceLive {
			message := toDaemonMessage(saved.Message)
			chat := toDaemonChat(saved.Chat)
			c.daemon.PublishNewMessage(message, chat)
			if c.notifier != nil && textInput.CountUnread && c.ShouldNotifyChat(chat.ID) {
				c.notifier.NotifyMessage(ctx, message, chat)
			}
		}
		return saved, true
	}

	if mediaInput, ok := c.imageMessageInput(ctx, evt, opts); ok {
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
		c.log.Infof("Stored media message %s from %s", saved.Message.ID, saved.Message.SenderID)
		if opts.source == sourceLive {
			message := toDaemonMessage(saved.Message)
			chat := toDaemonChat(saved.Chat)
			c.daemon.PublishNewMessage(message, chat)
			if c.notifier != nil && mediaInput.CountUnread && c.ShouldNotifyChat(chat.ID) {
				c.notifier.NotifyMessage(ctx, message, chat)
			}
		}
		return saved, true
	}

	return appstore.SavedTextMessage{}, false
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
			Timestamp:      info.Timestamp,
			Direction:      direction,
			Status:         status,
			IsGroup:        info.IsGroup,
			CountUnread:    shouldCountUnread(evt, opts),
		},
		MediaMimeType:           mimeType,
		MediaThumbnailLocalPath: thumbnailLocalPath,
		MediaWidth:              int32(imgMsg.GetWidth()),
		MediaHeight:             int32(imgMsg.GetHeight()),
		MediaPayload:            payload,
	}, true
}

func (c *Client) saveMessageThumbnail(chatID, messageID string, thumbnail []byte) string {
	if len(thumbnail) == 0 {
		return ""
	}
	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", chatID)
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		c.log.Warnf("Failed to create thumbnail cache directory for message %s: %v", messageID, err)
		return ""
	}
	localPath := filepath.Join(mediaDir, safeMediaFileName(messageID, ".thumb.jpg"))
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
		Timestamp:      info.Timestamp,
		Direction:      direction,
		Status:         status,
		IsGroup:        info.IsGroup,
		CountUnread:    shouldCountUnread(evt, opts),
	}, true
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
		return info.Sender.String()
	}
	return info.Chat.String()
}

func senderJID(info types.MessageInfo) types.JID {
	if info.IsFromMe {
		return types.JID{}
	}
	if !info.Sender.IsEmpty() {
		return info.Sender
	}
	return info.Chat
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
		if err := c.store.UpdateSenderName(ctx, jid.String(), name); err != nil {
			c.log.Warnf("Failed to store sender push name for %s: %v", jid, err)
		}
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

func (c *Client) senderName(ctx context.Context, jid types.JID) string {
	if jid.IsEmpty() {
		return ""
	}
	if name := c.contactNameForJID(ctx, jid); name != "" {
		return name
	}
	if phone := formatPhoneDisplayName(jid); phone != "" {
		return phone
	}
	return jid.User
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
		Direction:               message.Direction,
		Status:                  message.Status,
		MediaMimeType:           message.MediaMimeType,
		MediaLocalPath:          message.MediaLocalPath,
		MediaThumbnailLocalPath: message.MediaThumbnailLocalPath,
		MediaWidth:              message.MediaWidth,
		MediaHeight:             message.MediaHeight,
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
		AvatarLocalPath:      chat.AvatarLocalPath,
	}
}
