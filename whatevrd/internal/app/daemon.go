package app

import (
	"sync"
	"sync/atomic"
	"time"
)

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
	paths      Paths
	state      atomic.Int32
	nextSubID  atomic.Uint64
	subMu      sync.Mutex
	daemonSubs map[uint64]chan DaemonEvent
	loginSubs  map[uint64]chan LoginEvent
	latestQR   *QRCode
	lastDetail string

	retryAttempt  atomic.Int32
	nextRetryUnix atomic.Int64
	canReconnect  atomic.Bool

	droppedDaemonEvents atomic.Uint64
	droppedLoginEvents  atomic.Uint64
}

func NewDaemon(paths Paths) *Daemon {
	d := &Daemon{
		paths:      paths,
		daemonSubs: make(map[uint64]chan DaemonEvent),
		loginSubs:  make(map[uint64]chan LoginEvent),
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
	RetryAttempt   int32
	NextRetryUnix  int64
	CanReconnect   bool

	HistorySync HistorySyncEvent
}

type DaemonEventKind int

const (
	DaemonEventConnectionChanged DaemonEventKind = iota + 1
	DaemonEventNewMessage
	DaemonEventMessageUpdated
	DaemonEventChatUpdated
	DaemonEventChatPresence
	DaemonEventHistorySyncProgress
	DaemonEventHistoryBackfilled
)

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
)

type HistorySyncEvent struct {
	SyncType             HistorySyncType
	ProgressPercent      uint32
	ChunkOrder           uint32
	ConversationsInChunk uint32
	MessagesInChunk      uint32
	IsComplete           bool

	ChatID        string
	MessagesAdded uint32
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
	AvatarLocalPath      string
}

type Message struct {
	ID             string
	ChatID         string
	SenderID       string
	Text           string
	TimestampUnix  int64
	Direction      string
	Status         string
	MediaMimeType  string
	MediaLocalPath string
	MediaWidth     int32
	MediaHeight    int32
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
	ch := make(chan DaemonEvent, 16)

	d.subMu.Lock()
	d.daemonSubs[id] = ch
	state := State(d.state.Load())
	detail := d.lastDetail
	d.subMu.Unlock()

	ch <- DaemonEvent{
		Kind:          DaemonEventConnectionChanged,
		State:         state,
		Detail:        detail,
		RetryAttempt:  d.retryAttempt.Load(),
		NextRetryUnix: d.nextRetryUnix.Load(),
		CanReconnect:  d.canReconnect.Load(),
	}

	return ch, func() {
		d.subMu.Lock()
		delete(d.daemonSubs, id)
		d.subMu.Unlock()
	}
}

func (d *Daemon) SubscribeLoginEvents() (<-chan LoginEvent, func()) {
	id := d.nextSubID.Add(1)
	ch := make(chan LoginEvent, 16)

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
	d.broadcastDaemonEvent(DaemonEvent{
		Kind:        DaemonEventHistorySyncProgress,
		HistorySync: evt,
	})
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
	d.broadcastDaemonEvent(DaemonEvent{
		Kind:        DaemonEventChatPresence,
		Chat:        Chat{ID: chatID},
		SenderID:    senderID,
		IsComposing: isComposing,
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
