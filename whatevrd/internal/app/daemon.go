package app

import (
	"sync"
	"sync/atomic"
	"time"
)

type Status struct {
	State State
	Paths Paths
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

	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventConnectionChanged, State: state, Detail: detail})
	d.broadcastLoginEvent(LoginEvent{Kind: LoginEventState, State: state, Detail: detail})
}

func (d *Daemon) Status() Status {
	return Status{
		State: State(d.state.Load()),
		Paths: d.paths,
	}
}

type DaemonEvent struct {
	Kind    DaemonEventKind
	State   State
	Detail  string
	Message Message
	Chat    Chat
}

type DaemonEventKind int

const (
	DaemonEventConnectionChanged DaemonEventKind = iota + 1
	DaemonEventNewMessage
	DaemonEventChatUpdated
)

type Chat struct {
	ID              string
	Name            string
	LastMessage     string
	LastMessageTime int64
	UnreadCount     int32
	IsGroup         bool
}

type Message struct {
	ID            string
	ChatID        string
	SenderID      string
	Text          string
	TimestampUnix int64
	Direction     string
	Status        string
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

	ch <- DaemonEvent{Kind: DaemonEventConnectionChanged, State: state, Detail: detail}

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
		}
	}
}

func (d *Daemon) PublishNewMessage(message Message, chat Chat) {
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventNewMessage, Message: message})
	d.broadcastDaemonEvent(DaemonEvent{Kind: DaemonEventChatUpdated, Chat: chat})
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
		}
	}
}
