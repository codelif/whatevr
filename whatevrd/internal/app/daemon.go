package app

import (
	"sync"
	"sync/atomic"
	"time"
)

var composingPresenceTTL = 15 * time.Second

const daemonSubscriberBuffer = 256

type Status struct {
	State               State
	Detail              string
	Paths               Paths
	RetryAttempt        int32
	NextRetryUnix       int64
	CanReconnect        bool
	DroppedDaemonEvents uint64
	DroppedLoginEvents  uint64
}

type Daemon struct {
	paths             Paths
	state             atomic.Int32
	nextSubID         atomic.Uint64
	subMu             sync.Mutex
	daemonSubs        map[uint64]chan DaemonEvent
	loginSubs         map[uint64]chan LoginEvent
	latestQR          *QRCode
	lastDetail        string
	presenceByChatID  map[string]presenceState
	latestHistorySync *HistorySyncEvent
	mediaDownloads    map[string]MediaDownloadEvent

	retryAttempt  atomic.Int32
	nextRetryUnix atomic.Int64
	canReconnect  atomic.Bool

	droppedDaemonEvents atomic.Uint64
	droppedLoginEvents  atomic.Uint64
}

func NewDaemon(paths Paths) *Daemon {
	d := &Daemon{
		paths:            paths,
		daemonSubs:       make(map[uint64]chan DaemonEvent),
		loginSubs:        make(map[uint64]chan LoginEvent),
		presenceByChatID: make(map[string]presenceState),
		mediaDownloads:   make(map[string]MediaDownloadEvent),
	}
	d.SetState(StateStarting)
	return d
}

func (d *Daemon) SetState(state State) {
	d.SetStateDetail(state, "")
}

func (d *Daemon) SetStateDetail(state State, detail string) {
	d.state.Store(int32(state))

	d.subMu.Lock()
	d.lastDetail = detail
	d.subMu.Unlock()

	d.broadcastDaemonEvent(DaemonEvent{
		Kind:          DaemonEventConnectionChanged,
		State:         state,
		Detail:        detail,
		RetryAttempt:  d.retryAttempt.Load(),
		NextRetryUnix: d.nextRetryUnix.Load(),
		CanReconnect:  d.canReconnect.Load(),
	})
	d.broadcastLoginEvent(LoginEvent{Kind: LoginEventState, State: state, Detail: detail})
}

// SetConnMeta updates retry/reconnect metadata without changing state.
// Call before SetStateDetail so the next broadcast includes the new values.
func (d *Daemon) SetConnMeta(attempt int32, nextRetryUnix int64, canReconnect bool) {
	d.retryAttempt.Store(attempt)
	d.nextRetryUnix.Store(nextRetryUnix)
	d.canReconnect.Store(canReconnect)
}

func (d *Daemon) Status() Status {
	d.subMu.Lock()
	detail := d.lastDetail
	d.subMu.Unlock()

	return Status{
		State:               State(d.state.Load()),
		Detail:              detail,
		Paths:               d.paths,
		RetryAttempt:        d.retryAttempt.Load(),
		NextRetryUnix:       d.nextRetryUnix.Load(),
		CanReconnect:        d.canReconnect.Load(),
		DroppedDaemonEvents: d.droppedDaemonEvents.Load(),
		DroppedLoginEvents:  d.droppedLoginEvents.Load(),
	}
}

type DaemonEvent struct {
	Kind           DaemonEventKind
	State          State
	Detail         string
	Message        Message
	Chat           Chat
	PreviousChatID string
	SenderID       string
	IsComposing    bool
	Availability   ContactAvailability
	LastSeenUnix   int64
	// MessageDeleted payload (the row is gone, so only ids survive).
	DeletedChatID    string
	DeletedMessageID string
	RetryAttempt     int32
	NextRetryUnix    int64
	CanReconnect     bool

	HistorySync     HistorySyncEvent
	MediaDownload   MediaDownloadEvent
	Avatar          Avatar
	StickerSource   StickerSource
	StickerDownload StickerDownloadEvent
}

type presenceState struct {
	SenderID             string
	IsComposing          bool
	ComposingUpdatedTime time.Time
	Availability         ContactAvailability
	LastSeenUnix         int64
}

type ContactAvailability int32

const (
	ContactAvailabilityUnspecified ContactAvailability = iota
	ContactAvailabilityOnline
	ContactAvailabilityOffline
)

type DaemonEventKind int

const (
	DaemonEventConnectionChanged DaemonEventKind = iota + 1
	DaemonEventNewMessage
	DaemonEventMessageUpdated
	DaemonEventChatUpdated
	DaemonEventChatPresence
	DaemonEventHistorySyncProgress
	DaemonEventHistoryBackfilled
	DaemonEventMediaDownloadChanged
	DaemonEventAvatarUpdated
	DaemonEventStickerLibraryChanged
	DaemonEventStickerDownloadChanged
	DaemonEventMessageDeleted
)

type StickerSource int32

const (
	StickerSourceUnspecified StickerSource = iota
	StickerSourceRecent
	StickerSourceFavorite
	StickerSourceAll
)

type Sticker struct {
	CacheKey          string
	LocalPath         string
	MimeType          string
	IsAnimated        bool
	Width             int32
	Height            int32
	Emojis            []string
	AccessibilityText string
	PackID            string
	IsFavorite        bool
	LastUsedUnix      int64
	Weight            float32
}

type StickerDownloadEvent struct {
	Sticker   Sticker
	ErrorText string
}

type AvatarSubjectKind int32

const (
	AvatarSubjectKindUnspecified AvatarSubjectKind = iota
	AvatarSubjectKindChat
	AvatarSubjectKindSender
)

type Avatar struct {
	Kind          AvatarSubjectKind
	ID            string
	LocalPath     string
	Status        string
	UpdatedAtUnix int64
	Fetching      bool
}

type HistorySyncType int32

const (
	HistorySyncTypeUnspecified HistorySyncType = iota
	HistorySyncTypeInitialBootstrap
	HistorySyncTypeInitialStatusV3
	HistorySyncTypeFull
	HistorySyncTypeRecent
	HistorySyncTypePushName
	HistorySyncTypeNonBlockingData
	HistorySyncTypeOnDemand
	HistorySyncTypeProfilePicture
	HistorySyncTypeOfflineCatchup
)

type HistorySyncPhase int32

const (
	HistorySyncPhaseUnspecified HistorySyncPhase = iota
	HistorySyncPhaseQueued
	HistorySyncPhaseDownloading
	HistorySyncPhaseProcessing
	HistorySyncPhaseComplete
)

type HistorySyncEvent struct {
	SyncType               HistorySyncType
	ProgressPercent        uint32
	ChunkOrder             uint32
	ConversationsInChunk   uint32
	MessagesInChunk        uint32
	IsComplete             bool
	Phase                  HistorySyncPhase
	ProcessedConversations uint32
	ProcessedMessages      uint32

	ChatID        string
	MessagesAdded uint32
}

type MediaDownloadEvent struct {
	MessageID   string
	ChatID      string
	Downloading bool
	ErrorText   string
	// Streamed download progress; TotalBytes is 0 when the media metadata
	// does not carry a file length.
	ReceivedBytes uint64
	TotalBytes    uint64
}

type Chat struct {
	ID                   string
	Name                 string
	LastMessage          string
	LastMessageTime      int64
	LastMessageDirection string
	LastMessageStatus    string
	UnreadCount          int32
	IsGroup              bool
	IsPinned             bool
	PinnedOrder          uint32
	UpdatedAtUnix        int64
	AvatarLocalPath      string
}

type Message struct {
	ID                      string
	ChatID                  string
	SenderID                string
	SenderName              string
	SenderAvatarLocalPath   string
	Text                    string
	TimestampUnix           int64
	SortSeq                 int64
	Direction               string
	Status                  string
	MediaKind               string
	MediaMimeType           string
	MediaLocalPath          string
	MediaThumbnailLocalPath string
	MediaWidth              int32
	MediaHeight             int32
	MediaAnimated           bool
	MediaCacheKey           string
	IsRevoked               bool
	IsEdited                bool
	ReplyTo                 MessageReply
	Reactions               []Reaction
}

type MessageReply struct {
	MessageID     string
	SenderID      string
	SenderName    string
	Text          string
	MediaKind     string
	MediaMimeType string
	Direction     string
}

type Reaction struct {
	Emoji         string
	SenderID      string
	SenderName    string
	TimestampUnix int64
	FromMe        bool
}

// ParticipantReceipt is one group member's receipt state for a message; zero
// timestamps mean the member hasn't reached that state yet.
type ParticipantReceipt struct {
	JID             string
	DisplayName     string
	AvatarLocalPath string
	DeliveredTsUnix int64
	ReadTsUnix      int64
	PlayedTsUnix    int64
}

type MessageInfo struct {
	Status          string
	SentTsUnix      int64
	DeliveredTsUnix int64
	ReadTsUnix      int64
	IsGroup         bool
	Receipts        []ParticipantReceipt
}

type LoginEventKind int

const (
	LoginEventState LoginEventKind = iota + 1
	LoginEventQR
)

type QRCode struct {
	Code      string
	ExpiresAt time.Time
}

type LoginEvent struct {
	Kind      LoginEventKind
	State     State
	Detail    string
	QRCode    string
	ExpiresAt time.Time
}

func (d *Daemon) PublishQRCode(code string, expiresAt time.Time) {
	d.subMu.Lock()
	d.latestQR = &QRCode{Code: code, ExpiresAt: expiresAt}
	d.subMu.Unlock()

	d.broadcastLoginEvent(LoginEvent{Kind: LoginEventQR, QRCode: code, ExpiresAt: expiresAt})
}

func (d *Daemon) SubscribeDaemonEvents() (<-chan DaemonEvent, func()) {
	id := d.nextSubID.Add(1)
	ch := make(chan DaemonEvent, daemonSubscriberBuffer)

	d.subMu.Lock()
	d.daemonSubs[id] = ch
	state := State(d.state.Load())
	detail := d.lastDetail
	latestHistorySync := d.latestHistorySync
	mediaDownloads := make([]MediaDownloadEvent, 0, len(d.mediaDownloads))
	for _, download := range d.mediaDownloads {
		mediaDownloads = append(mediaDownloads, download)
	}
	d.subMu.Unlock()

	ch <- DaemonEvent{
		Kind:          DaemonEventConnectionChanged,
		State:         state,
		Detail:        detail,
		RetryAttempt:  d.retryAttempt.Load(),
		NextRetryUnix: d.nextRetryUnix.Load(),
		CanReconnect:  d.canReconnect.Load(),
	}
	if latestHistorySync != nil {
		ch <- DaemonEvent{Kind: DaemonEventHistorySyncProgress, HistorySync: *latestHistorySync}
	}
	for _, download := range mediaDownloads {
		ch <- DaemonEvent{Kind: DaemonEventMediaDownloadChanged, MediaDownload: download}
	}

	return ch, func() {
		d.subMu.Lock()
		delete(d.daemonSubs, id)
		d.subMu.Unlock()
	}
}

func (d *Daemon) SubscribeLoginEvents() (<-chan LoginEvent, func()) {
	id := d.nextSubID.Add(1)
	ch := make(chan LoginEvent, 32)

	d.subMu.Lock()
	d.loginSubs[id] = ch
	state := State(d.state.Load())
	detail := d.lastDetail
	latestQR := d.latestQR
	d.subMu.Unlock()

	ch <- LoginEvent{Kind: LoginEventState, State: state, Detail: detail}
	if latestQR != nil && time.Now().Before(latestQR.ExpiresAt) {
		ch <- LoginEvent{Kind: LoginEventQR, QRCode: latestQR.Code, ExpiresAt: latestQR.ExpiresAt}
	}

	return ch, func() {
		d.subMu.Lock()
		delete(d.loginSubs, id)
		d.subMu.Unlock()
	}
}

func (d *Daemon) broadcastDaemonEvent(event DaemonEvent) {
	d.subMu.Lock()
	subs := make([]chan DaemonEvent, 0, len(d.daemonSubs))
	for _, ch := range d.daemonSubs {
		subs = append(subs, ch)
	}
	d.subMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			d.droppedDaemonEvents.Add(1)
		}
	}
}

func (d *Daemon) PublishNewMessage(message Message, chat Chat) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventNewMessage, Message: message})
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChatUpdated, Chat: chat})
}

func (d *Daemon) PublishMessageUpdated(message Message) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventMessageUpdated, Message: message})
}

func (d *Daemon) PublishMessageDeleted(chatID, messageID string, chat Chat) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventMessageDeleted, DeletedChatID: chatID, DeletedMessageID: messageID})
	if chat.ID != "" {
		d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChatUpdated, Chat: chat})
	}
}

type FrontendSessionController interface {
	FrontendSessionStarted(string)
	FrontendSessionEnded(string)
	FrontendSessionStateChanged(string, bool, string)
}

func (d *Daemon) PublishChatUpdated(chat Chat) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChatUpdated, Chat: chat})
}

func (d *Daemon) PublishChatMigrated(previousChatID string, chat Chat) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChatUpdated, Chat: chat, PreviousChatID: previousChatID})
}

func (d *Daemon) PublishHistorySyncProgress(evt HistorySyncEvent) {
	d.subMu.Lock()
	if evt.IsComplete {
		d.latestHistorySync = nil
	} else {
		copy := evt
		d.latestHistorySync = &copy
	}
	d.subMu.Unlock()

	d.broadcastDaemonEvent(DaemonEvent{
		Kind:        DaemonEventHistorySyncProgress,
		HistorySync: evt,
	})
}

func (d *Daemon) HasActiveHistorySync() bool {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	return d.latestHistorySync != nil
}

func (d *Daemon) PublishHistoryBackfilled(chatID string, messagesAdded uint32) {
	d.broadcastDaemonEvent(DaemonEvent{
		Kind: DaemonEventHistoryBackfilled,
		HistorySync: HistorySyncEvent{
			ChatID:        chatID,
			MessagesAdded: messagesAdded,
		},
	})
}

func (d *Daemon) PublishChatPresence(chatID, senderID string, isComposing bool) {
	updatedAt := time.Now()

	d.subMu.Lock()
	state := d.presenceByChatID[chatID]
	state.SenderID = senderID
	state.IsComposing = isComposing
	state.ComposingUpdatedTime = updatedAt
	d.presenceByChatID[chatID] = state
	d.subMu.Unlock()

	d.broadcastDaemonEvent(DaemonEvent{
		Kind:        DaemonEventChatPresence,
		Chat:        Chat{ID: chatID},
		SenderID:    senderID,
		IsComposing: isComposing,
	})

	if isComposing {
		d.scheduleComposingPresenceExpiry(chatID, senderID, updatedAt)
	}
}

func (d *Daemon) ClearChatComposing(chatID, senderID string) bool {
	d.subMu.Lock()
	state, ok := d.presenceByChatID[chatID]
	if !ok || !state.IsComposing || state.SenderID != senderID {
		d.subMu.Unlock()
		return false
	}
	state.IsComposing = false
	d.presenceByChatID[chatID] = state
	d.subMu.Unlock()

	d.broadcastDaemonEvent(DaemonEvent{
		Kind:        DaemonEventChatPresence,
		Chat:        Chat{ID: chatID},
		SenderID:    senderID,
		IsComposing: false,
	})
	return true
}

func (d *Daemon) scheduleComposingPresenceExpiry(chatID, senderID string, updatedAt time.Time) {
	// AfterFunc keeps the expiry off a parked goroutine; firing is idempotent
	// because expireComposingPresence checks the update timestamp.
	time.AfterFunc(composingPresenceTTL, func() {
		d.expireComposingPresence(chatID, senderID, updatedAt)
	})
}

func (d *Daemon) expireComposingPresence(chatID, senderID string, updatedAt time.Time) {
	d.subMu.Lock()
	state, ok := d.presenceByChatID[chatID]
	if !ok || !state.IsComposing || state.SenderID != senderID || !state.ComposingUpdatedTime.Equal(updatedAt) {
		d.subMu.Unlock()
		return
	}
	state.IsComposing = false
	d.presenceByChatID[chatID] = state
	d.subMu.Unlock()

	d.broadcastDaemonEvent(DaemonEvent{
		Kind:        DaemonEventChatPresence,
		Chat:        Chat{ID: chatID},
		SenderID:    senderID,
		IsComposing: false,
	})
}

func (d *Daemon) PublishContactAvailability(chatID string, availability ContactAvailability, lastSeenUnix int64) {
	d.subMu.Lock()
	state := d.presenceByChatID[chatID]
	state.Availability = availability
	if lastSeenUnix > 0 {
		state.LastSeenUnix = lastSeenUnix
	}
	d.presenceByChatID[chatID] = state
	d.subMu.Unlock()

	d.broadcastDaemonEvent(DaemonEvent{
		Kind:         DaemonEventChatPresence,
		Chat:         Chat{ID: chatID},
		Availability: availability,
		LastSeenUnix: lastSeenUnix,
	})
}

func (d *Daemon) PublishCachedChatPresence(chatID string) bool {
	d.subMu.Lock()
	state, ok := d.presenceByChatID[chatID]
	d.subMu.Unlock()
	if !ok {
		return false
	}

	if state.IsComposing && time.Since(state.ComposingUpdatedTime) > composingPresenceTTL {
		state.IsComposing = false
	}

	if state.Availability != ContactAvailabilityUnspecified || state.LastSeenUnix > 0 {
		d.broadcastDaemonEvent(DaemonEvent{
			Kind:         DaemonEventChatPresence,
			Chat:         Chat{ID: chatID},
			Availability: state.Availability,
			LastSeenUnix: state.LastSeenUnix,
		})
	}
	if state.IsComposing {
		d.broadcastDaemonEvent(DaemonEvent{
			Kind:        DaemonEventChatPresence,
			Chat:        Chat{ID: chatID},
			SenderID:    state.SenderID,
			IsComposing: state.IsComposing,
		})
	}
	return true
}

func (d *Daemon) PublishMediaDownloadChanged(messageID, chatID string, downloading bool, errorText string, receivedBytes, totalBytes uint64) {
	evt := MediaDownloadEvent{MessageID: messageID, ChatID: chatID, Downloading: downloading, ErrorText: errorText, ReceivedBytes: receivedBytes, TotalBytes: totalBytes}
	d.subMu.Lock()
	if downloading {
		d.mediaDownloads[messageID] = evt
	} else {
		delete(d.mediaDownloads, messageID)
	}
	d.subMu.Unlock()

	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventMediaDownloadChanged, MediaDownload: evt})
}

func (d *Daemon) PublishAvatarUpdated(avatar Avatar) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventAvatarUpdated, Avatar: avatar})
}

func (d *Daemon) PublishStickerLibraryChanged(source StickerSource) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventStickerLibraryChanged, StickerSource: source})
}

func (d *Daemon) PublishStickerDownloadChanged(sticker Sticker, errorText string) {
	d.broadcastDaemonEvent(DaemonEvent{
		Kind:            DaemonEventStickerDownloadChanged,
		StickerDownload: StickerDownloadEvent{Sticker: sticker, ErrorText: errorText},
	})
}

func (d *Daemon) broadcastLoginEvent(event LoginEvent) {
	d.subMu.Lock()
	subs := make([]chan LoginEvent, 0, len(d.loginSubs))
	for _, ch := range d.loginSubs {
		subs = append(subs, ch)
	}
	d.subMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			d.droppedLoginEvents.Add(1)
		}
	}
}
