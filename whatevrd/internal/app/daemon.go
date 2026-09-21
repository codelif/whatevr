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

// connState is the whole of what the daemon says about the connection. It is
// one value behind one lock on purpose: as five independent atomics with three
// writer goroutines, a reader could see the state from one event and the detail
// from another, and publish "Offline" alongside "Connected to WhatsApp".
type connState struct {
	state         State
	detail        string
	retryAttempt  int32
	nextRetryUnix int64
	canReconnect  bool
}

type Daemon struct {
	paths             Paths
	nextSubID         atomic.Uint64
	subMu             sync.Mutex
	daemonSubs        map[uint64]chan DaemonEvent
	loginSubs         map[uint64]chan LoginEvent
	latestQR          *QRCode
	presenceByChatID  map[string]presenceState
	latestHistorySync *HistorySyncEvent
	mediaDownloads    map[string]MediaDownloadEvent
	ringingCalls      map[string]RingingCall

	connMu sync.Mutex
	conn   connState

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

// ResetAccountState drops the account-scoped snapshots the daemon replays to a
// new subscriber. Without it the previous account's QR, typing indicators,
// history-sync progress and in-flight downloads were handed to the next
// account's first frontend.
func (d *Daemon) ResetAccountState() {
	d.subMu.Lock()
	d.latestQR = nil
	d.latestHistorySync = nil
	d.presenceByChatID = make(map[string]presenceState)
	d.mediaDownloads = make(map[string]MediaDownloadEvent)
	d.subMu.Unlock()
}

func (d *Daemon) SetState(state State) {
	d.SetStateDetail(state, "")
}

func (d *Daemon) SetStateDetail(state State, detail string) {
	d.connMu.Lock()
	d.conn.state = state
	d.conn.detail = detail
	snapshot := d.conn
	d.connMu.Unlock()

	d.publishConnState(snapshot)
}

// SetConnMeta updates retry/reconnect metadata without changing state.
// Call before SetStateDetail so the next broadcast includes the new values.
//
// Prefer SetConnection where both change: two calls leave a window in which a
// reader sees the new metadata against the old state.
func (d *Daemon) SetConnMeta(attempt int32, nextRetryUnix int64, canReconnect bool) {
	d.connMu.Lock()
	d.conn.retryAttempt = attempt
	d.conn.nextRetryUnix = nextRetryUnix
	d.conn.canReconnect = canReconnect
	d.connMu.Unlock()
}

// SetConnection publishes state, detail and retry metadata as one change, so
// no reader can catch the connection half-updated.
func (d *Daemon) SetConnection(state State, detail string, attempt int32, nextRetryUnix int64, canReconnect bool) {
	d.connMu.Lock()
	d.conn = connState{
		state:         state,
		detail:        detail,
		retryAttempt:  attempt,
		nextRetryUnix: nextRetryUnix,
		canReconnect:  canReconnect,
	}
	snapshot := d.conn
	d.connMu.Unlock()

	d.publishConnState(snapshot)
}

func (d *Daemon) publishConnState(snapshot connState) {
	d.broadcastDaemonEvent(DaemonEvent{
		Kind:          DaemonEventConnectionChanged,
		State:         snapshot.state,
		Detail:        snapshot.detail,
		RetryAttempt:  snapshot.retryAttempt,
		NextRetryUnix: snapshot.nextRetryUnix,
		CanReconnect:  snapshot.canReconnect,
	})
	d.broadcastLoginEvent(LoginEvent{Kind: LoginEventState, State: snapshot.state, Detail: snapshot.detail})
}

func (d *Daemon) Status() Status {
	d.connMu.Lock()
	snapshot := d.conn
	d.connMu.Unlock()

	return Status{
		State:               snapshot.state,
		Detail:              snapshot.detail,
		Paths:               d.paths,
		RetryAttempt:        snapshot.retryAttempt,
		NextRetryUnix:       snapshot.nextRetryUnix,
		CanReconnect:        snapshot.canReconnect,
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
	// CallID identifies the call a DaemonEventCallChanged is about.
	CallID        string
	RetryAttempt  int32
	NextRetryUnix int64
	CanReconnect  bool

	HistorySync     HistorySyncEvent
	MediaDownload   MediaDownloadEvent
	Avatar          Avatar
	StickerSource   StickerSource
	StickerDownload StickerDownloadEvent

	// Second-phase enrichment for contact/group info (see ContactInfo/GroupInfo).
	ContactInfo ContactInfo
	GroupInfo   GroupInfo
	GroupChatID string

	// Privacy snapshot for DaemonEventPrivacySettingsChanged.
	PrivacySettings PrivacySettings
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
	DaemonEventContactInfoUpdated
	DaemonEventGroupInfoUpdated
	DaemonEventPrivacySettingsChanged
	DaemonEventSelfProfileChanged
	DaemonEventBlocklistChanged
	DaemonEventChatDeleted
	DaemonEventChatCleared
	// DaemonEventMessageReceipt fires whenever a per-participant receipt is
	// recorded for one of our messages, even when it does not advance the
	// message's aggregate status (a single group member's receipt usually does
	// not). It carries only the message + chat ids; the `receipts` view keys off
	// it to re-derive the per-member breakdown, and views that only track message
	// status ignore it.
	DaemonEventMessageReceipt
	// DaemonEventIdentityChanged fires when a contact's WhatsApp identity key
	// (security code) changes — they reinstalled or re-registered — and the
	// daemon accepted the new identity. Carries the contact jid in SenderID.
	// No view consumes it yet; it exists so a "security code changed" notice
	// can be rendered without a daemon change.
	DaemonEventIdentityChanged
	// DaemonEventPreferencesChanged fires when the daemon-persisted app
	// preferences change (via SetAppPreferences). It carries no payload; the
	// `preferences` view re-reads GetAppPreferences off it.
	DaemonEventPreferencesChanged
	// DaemonEventStatusChanged fires when a contact status (story) arrives or
	// updates. It carries no payload; the `status` view re-reads the store.
	DaemonEventStatusChanged
	// DaemonEventCallChanged fires when a call starts or stops ringing. CallID
	// + Chat identify the call; the `calls` view re-reads the ringing set off
	// it (the event itself carries only identity, like MessageReceipt).
	DaemonEventCallChanged
	// DaemonEventChannelsChanged fires when the followed-channel directory
	// refreshes. It carries no payload; the `channels` view re-reads the
	// store.
	DaemonEventChannelsChanged
	// DaemonEventLiveLocationsChanged fires when a live-location share in a
	// chat opens, moves or ends. Its own view exists because a position that
	// changes every few seconds has no business sharing an item with a message
	// row that does not (PROTOCOL.md, Granularity).
	DaemonEventLiveLocationsChanged
	// DaemonEventResync is a synthetic sentinel the broadcaster posts to a
	// subscriber whose buffer overflowed: rather than silently dropping events
	// (which permanently desyncs a view that folds events into local state), the
	// broadcaster coalesces the whole backlog into this one event. A consumer
	// treats it as "you missed events, reload from source" — re-read the store /
	// re-fetch the snapshot and re-emit. It mirrors the wire-level purge+reset
	// for slow consumers (queue.go), one layer up (daemon→session).
	DaemonEventResync
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
	// The slot after OnDemand was HistorySyncTypeProfilePicture, removed with
	// the bulk avatar prefetch; keep numbering stable for OfflineCatchup.
	historySyncTypeRetiredProfilePicture //nolint:unused
	HistorySyncTypeOfflineCatchup
)

type HistorySyncPhase int32

const (
	HistorySyncPhaseUnspecified HistorySyncPhase = iota
	HistorySyncPhaseQueued
	HistorySyncPhaseDownloading
	HistorySyncPhaseProcessing
	HistorySyncPhaseComplete
	// The phone stopped delivering chunks below 100% and has been idle past
	// the stall timeout; the sync may still resume later.
	HistorySyncPhaseStalled
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

// MediaStream is the answer to `media.stream`: where a player can read a
// message's media from while the daemon is still fetching it. URL points at the
// daemon's loopback range server and is only valid for the current daemon
// process.
type MediaStream struct {
	StreamID     string
	URL          string
	Mime         string
	SizeBytes    uint64
	DurationSecs int32
}

// MediaStreamUpdate is the one terminal recovery result for a stream request.
// It is delivered to the protocol connection that requested StreamID.
type MediaStreamUpdate struct {
	StreamID  string
	MessageID string
	State     string
	Path      string
	ErrorText string
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
	IsFavorite           bool
	IsArchived           bool
	IsMuted              bool
	MuteEndTimestamp     int64
	HistoryExhausted     bool
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
	SortMS                  int64
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
	// Preview is the message rendered as one human-readable line, computed
	// once by the store so the notifier, the chat row and the wire `fallback`
	// cannot say three different things about the same message.
	Preview         string
	IsRevoked       bool
	IsEdited        bool
	IsStarred       bool
	PinnedUntilUnix int64
	ReplyTo         MessageReply
	Reactions       []Reaction
	Mentions        []Mention
}

type Mention struct {
	JID         string
	DisplayName string
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

// PhoneCheck reports whether a typed phone number is registered on WhatsApp.
type PhoneCheck struct {
	Registered  bool
	JID         string
	DisplayName string
	IsBusiness  bool
	Phone       string
}

// ContactInfo is the full contact card for a 1:1 user.
type ContactInfo struct {
	JID             string
	PhoneNumber     string
	SavedName       string
	PushName        string
	BusinessName    string
	AvatarLocalPath string
	IsBusiness      bool
	StatusText      string
}

// GroupMember is one resolved group participant.
type GroupMember struct {
	JID             string
	DisplayName     string
	PhoneNumber     string
	AvatarLocalPath string
	IsAdmin         bool
	IsSuperAdmin    bool
}

// GroupRoleString maps a participant's admin flags to the wire role vocabulary
// shared by GroupInfo.MyRole and the group_members view: "superadmin", "admin",
// or "member".
func GroupRoleString(isAdmin, isSuperAdmin bool) string {
	switch {
	case isSuperAdmin:
		return "superadmin"
	case isAdmin:
		return "admin"
	default:
		return "member"
	}
}

// GroupInfo is the full card for a group chat. Owner/MyRole/IsAnnounce/IsLocked
// come only from the live GetGroupInfo fetch (the stored-participant fallback
// leaves them zero), so they populate in the second phase — see GetGroupInfo.
type GroupInfo struct {
	Subject         string
	Description     string
	AvatarLocalPath string
	CreatedUnix     int64
	OwnerJID        string
	// MyRole is the local account's role in the group: "member", "admin", or
	// "superadmin" (empty until the live fetch resolves it). Feeds composer
	// lockout together with IsAnnounce.
	MyRole     string
	IsAnnounce bool
	IsLocked   bool
	Members    []GroupMember
}

// PrivacySettings mirrors the user's WhatsApp privacy categories. Each audience
// field holds the raw whatsmeow value ("all", "contacts", "contact_blacklist",
// "none", "match_last_seen", "known"); ReadReceipts is a plain toggle because
// WhatsApp only allows everyone/nobody for it.
type PrivacySettings struct {
	LastSeen     string
	Online       string
	ProfilePhoto string
	About        string
	ReadReceipts bool
	GroupAdd     string
	CallAdd      string
}

// BlockedContact is one entry in the user's blocklist, with display fields
// resolved the same way chat participants are.
type BlockedContact struct {
	JID             string
	DisplayName     string
	PhoneNumber     string
	AvatarLocalPath string
}

// AppPreferences are the daemon-persisted preferences not tied to the WhatsApp
// account: notification gating and media auto-download policy.
type AppPreferences struct {
	NotificationsEnabled  bool
	NotificationSound     bool
	NotificationPreview   bool
	AutoDownloadPhotos    bool
	AutoDownloadVideos    bool
	AutoDownloadAudio     bool
	AutoDownloadDocuments bool
	AutoDownloadStickers  bool
	// AutoDownloadMaxBytes caps what auto-download will fetch on its own. 0
	// means no limit. It exists so a 200 MB video is a decision rather than a
	// side effect of scrolling past it.
	AutoDownloadMaxBytes int64
	// AntiDelete keeps the content of messages deleted for everyone and shows
	// it with a Deleted mark instead of a tombstone. Local-only display: it
	// changes nothing on the wire, so it cannot get the account flagged.
	AntiDelete bool
	// SendTypingIndicators announces composing presence while typing.
	// Turning it off just omits the announcement (passive); it changes no
	// message content or timing.
	SendTypingIndicators bool
	// AutoFetchMaps lets the daemon draw a real map for a shared location by
	// fetching tiles. On by default, because a location bubble without a map is
	// a pair of numbers. Turning it off means nothing tells a tile server you
	// received a location, and the bubble falls back to the small JPEG the
	// sender embedded.
	AutoFetchMaps bool
}

// DefaultAppPreferences are applied the first time the daemon runs, before the
// user has saved anything. Notifications on (no sound, with preview) and media
// auto-download off match WhatsApp Desktop defaults.
func DefaultAppPreferences() AppPreferences {
	return AppPreferences{
		NotificationsEnabled: true,
		NotificationSound:    false,
		NotificationPreview:  true,
		AutoDownloadMaxBytes: 16 * 1024 * 1024,
		AntiDelete:           true,
		SendTypingIndicators: true,
		AutoFetchMaps:        true,
	}
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

	// One snapshot, so the replayed event cannot mix a state from one moment
	// with retry metadata from another.
	d.connMu.Lock()
	conn := d.conn
	d.connMu.Unlock()

	d.subMu.Lock()
	latestHistorySync := d.latestHistorySync
	mediaDownloads := make([]MediaDownloadEvent, 0, len(d.mediaDownloads))
	for _, download := range d.mediaDownloads {
		mediaDownloads = append(mediaDownloads, download)
	}
	ch := make(chan DaemonEvent, daemonSubscriberBuffer+1+len(mediaDownloads)+boolToInt(latestHistorySync != nil))
	// Queue the replay before releasing the producer lock. Otherwise a live
	// event can overtake this snapshot and regress the subscriber's state.
	ch <- DaemonEvent{
		Kind:          DaemonEventConnectionChanged,
		State:         conn.state,
		Detail:        conn.detail,
		RetryAttempt:  conn.retryAttempt,
		NextRetryUnix: conn.nextRetryUnix,
		CanReconnect:  conn.canReconnect,
	}
	if latestHistorySync != nil {
		ch <- DaemonEvent{Kind: DaemonEventHistorySyncProgress, HistorySync: *latestHistorySync}
	}
	for _, download := range mediaDownloads {
		ch <- DaemonEvent{Kind: DaemonEventMediaDownloadChanged, MediaDownload: download}
	}
	d.daemonSubs[id] = ch
	d.subMu.Unlock()

	return ch, func() {
		d.subMu.Lock()
		delete(d.daemonSubs, id)
		d.subMu.Unlock()
	}
}

func (d *Daemon) SubscribeLoginEvents() (<-chan LoginEvent, func()) {
	id := d.nextSubID.Add(1)
	ch := make(chan LoginEvent, 32)

	d.connMu.Lock()
	conn := d.conn
	d.connMu.Unlock()

	d.subMu.Lock()
	d.loginSubs[id] = ch
	latestQR := d.latestQR
	d.subMu.Unlock()

	ch <- LoginEvent{Kind: LoginEventState, State: conn.state, Detail: conn.detail}
	if latestQR != nil && time.Now().Before(latestQR.ExpiresAt) {
		ch <- LoginEvent{Kind: LoginEventQR, QRCode: latestQR.Code, ExpiresAt: latestQR.ExpiresAt}
	}

	return ch, func() {
		d.subMu.Lock()
		delete(d.loginSubs, id)
		d.subMu.Unlock()
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (d *Daemon) broadcastDaemonEvent(event DaemonEvent) {
	// Hold subMu across delivery so the overflow path (drain + post a resync
	// sentinel) is the only producer touching a given channel: with no
	// concurrent producer, once the drain sees the channel empty it can post the
	// resync without racing another broadcast for the slot. Sends never block
	// (non-blocking select, and the overflow path makes room by draining), so a
	// slow consumer cannot stall the broadcaster or an unsubscribe.
	d.subMu.Lock()
	defer d.subMu.Unlock()
	for _, ch := range d.daemonSubs {
		select {
		case ch <- event:
		default:
			// Slow consumer: coalesce its backlog into a single resync sentinel
			// so it reloads from source instead of folding over a dropped-event
			// gap. Mirrors the wire-level purge+reset (queue.go) one layer up.
			d.droppedDaemonEvents.Add(1)
			drainAndPostResync(ch)
		}
	}
}

// drainAndPostResync empties ch and leaves a single DaemonEventResync in it. The
// caller must hold subMu so no other producer refills ch between the drain and
// the post; the consumer only receives, so once the channel reads empty it stays
// empty until the resync is posted (which then always fits).
func drainAndPostResync(ch chan DaemonEvent) {
	for {
		select {
		case <-ch:
		default:
			ch <- DaemonEvent{Kind: DaemonEventResync}
			return
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

// PublishMessageReceipt signals that a participant receipt was recorded for a
// message; see DaemonEventMessageReceipt. The event carries only ids so the
// `receipts` view re-derives from the store — the definitive receipt state.
func (d *Daemon) PublishMessageReceipt(chatID, messageID string) {
	d.broadcastDaemonEvent(DaemonEvent{
		Kind:    DaemonEventMessageReceipt,
		Chat:    Chat{ID: chatID},
		Message: Message{ID: messageID},
	})
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

func (d *Daemon) PublishChatDeleted(chatID string) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChatDeleted, DeletedChatID: chatID})
}

func (d *Daemon) PublishChatCleared(chat Chat) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChatCleared, Chat: chat})
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

// ComposingChat is one chat with an active (non-expired) composing sender.
type ComposingChat struct {
	ChatID   string
	SenderID string
}

// ComposingChats snapshots the chats a sender is currently composing in, for
// the `typing` view's initial fill. Entries past the composing TTL are treated
// as stopped and omitted (the expiry timer may not have fired yet).
func (d *Daemon) ComposingChats() []ComposingChat {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	var out []ComposingChat
	for chatID, state := range d.presenceByChatID {
		if state.IsComposing && time.Since(state.ComposingUpdatedTime) <= composingPresenceTTL {
			out = append(out, ComposingChat{ChatID: chatID, SenderID: state.SenderID})
		}
	}
	return out
}

// ChatAvailability snapshots the cached availability/last-seen for a chat, for
// the `presence` view's initial fill. ok is false until WhatsApp has delivered
// availability for the chat (which only happens once something has subscribed
// to its presence upstream), mirroring PublishCachedChatPresence's guard. The
// composing half of the presence state belongs to the `typing` view; this
// accessor is the availability half's counterpart to ComposingChats.
func (d *Daemon) ChatAvailability(chatID string) (ContactAvailability, int64, bool) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	state, ok := d.presenceByChatID[chatID]
	if !ok || (state.Availability == ContactAvailabilityUnspecified && state.LastSeenUnix == 0) {
		return ContactAvailabilityUnspecified, 0, false
	}
	return state.Availability, state.LastSeenUnix, true
}

// ActiveMediaDownloads snapshots the in-flight downloads, for the `transfers`
// view to rebuild its fold state on a resync (the same set the subscribe replay
// seeds it with initially).
func (d *Daemon) ActiveMediaDownloads() []MediaDownloadEvent {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	out := make([]MediaDownloadEvent, 0, len(d.mediaDownloads))
	for _, download := range d.mediaDownloads {
		out = append(out, download)
	}
	return out
}

// RingingCall is one locally-ringing call, for the `calls` view's initial
// fill. The desktop cannot answer (no media stack upstream); these exist to
// render, reject, and tombstone as missed.
type RingingCall struct {
	CallID      string
	ChatID      string
	CallerID    string
	Video       bool
	StartedUnix int64
}

// RingingCalls snapshots the currently ringing calls.
func (d *Daemon) RingingCalls() []RingingCall {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	out := make([]RingingCall, 0, len(d.ringingCalls))
	for _, call := range d.ringingCalls {
		out = append(out, call)
	}
	return out
}

// SetRingingCall records a ringing call; ClearRingingCall forgets it.
func (d *Daemon) SetRingingCall(call RingingCall) {
	if call.CallID == "" {
		return
	}
	d.subMu.Lock()
	defer d.subMu.Unlock()
	if d.ringingCalls == nil {
		d.ringingCalls = make(map[string]RingingCall)
	}
	d.ringingCalls[call.CallID] = call
}

func (d *Daemon) ClearRingingCall(callID string) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	delete(d.ringingCalls, callID)
}

// ConnectionSnapshot returns the current connection state the subscribe replay's
// ConnectionChanged carries, for the `connection` view to reload on a resync.
func (d *Daemon) ConnectionSnapshot() (state State, detail string, attempt int32, nextRetryUnix int64, canReconnect bool) {
	d.connMu.Lock()
	defer d.connMu.Unlock()
	return d.conn.state, d.conn.detail, d.conn.retryAttempt, d.conn.nextRetryUnix, d.conn.canReconnect
}

// LatestHistorySync returns the last history-sync progress event (ok=false if
// none), for the `sync` view to reload on a resync.
func (d *Daemon) LatestHistorySync() (HistorySyncEvent, bool) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	if d.latestHistorySync == nil {
		return HistorySyncEvent{}, false
	}
	return *d.latestHistorySync, true
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

// PublishContactInfoUpdated delivers the network-fetched status text after the
// contact card was already returned from local data.
func (d *Daemon) PublishContactInfoUpdated(info ContactInfo) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventContactInfoUpdated, ContactInfo: info})
}

// PublishPrivacySettingsChanged delivers a fresh privacy snapshot so an open
// settings window updates live (e.g. after a change made on the phone).
func (d *Daemon) PublishPrivacySettingsChanged(settings PrivacySettings) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventPrivacySettingsChanged, PrivacySettings: settings})
}

// PublishSelfProfileChanged signals that the user's own profile (name, about,
// avatar) changed, so the client re-fetches it.
func (d *Daemon) PublishSelfProfileChanged() {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventSelfProfileChanged})
}

// PublishBlocklistChanged signals that the blocklist changed, so the client
// re-fetches it.
func (d *Daemon) PublishBlocklistChanged() {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventBlocklistChanged})
}

// PublishPreferencesChanged signals that the daemon-persisted app preferences
// changed, so an open `preferences` view re-reads them.
func (d *Daemon) PublishPreferencesChanged() {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventPreferencesChanged})
}

// PublishStatusChanged signals a new or updated contact status; the `status`
// view re-reads the store off it.
func (d *Daemon) PublishStatusChanged() {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventStatusChanged})
}

// PublishCallChanged signals a call starting or stopping ringing; the
// `calls` view re-reads the ringing set off it.
func (d *Daemon) PublishCallChanged(callID, chatID string) {
	d.broadcastDaemonEvent(DaemonEvent{
		Kind:   DaemonEventCallChanged,
		CallID: callID,
		Chat:   Chat{ID: chatID},
	})
}

// PublishChannelsChanged signals a refreshed channel directory; the
// `channels` view re-reads the store off it.
func (d *Daemon) PublishChannelsChanged() {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChannelsChanged})
}

// PublishLiveLocationsChanged signals that a chat's set of running
// live-location shares changed, so an open `live_locations` view re-reads it.
func (d *Daemon) PublishLiveLocationsChanged(chatID string) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventLiveLocationsChanged, Chat: Chat{ID: chatID}})
}

// PublishIdentityChanged signals that a contact's WhatsApp identity (security
// code) changed and the new identity was accepted.
func (d *Daemon) PublishIdentityChanged(jid string) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventIdentityChanged, SenderID: jid})
}

// PublishGroupInfoUpdated delivers the live-fetched group fields (subject,
// description, member roles, creation time) after the stored card was returned.
func (d *Daemon) PublishGroupInfoUpdated(chatID string, info GroupInfo) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventGroupInfoUpdated, GroupChatID: chatID, GroupInfo: info})
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
