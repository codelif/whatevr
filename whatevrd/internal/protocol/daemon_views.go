package protocol

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"whatevrd/internal/app"
)

const objectViewSort = "0"

// PendingOutgoingCounter provides the connection view's pending queue count.
type PendingOutgoingCounter interface {
	CountPendingOutgoingMessages(context.Context) (int, error)
}

// DaemonStore is the daemon-owned state the built-in views read. *store.DB
// implements it; tests can pass nil when a view they exercise does not touch
// it.
type DaemonStore interface {
	PendingOutgoingCounter
	ChatLister
	MessageLister
	SenderDisplayer
}

// DaemonActions is the WhatsApp-client seam the protocol views call into — the
// upstream work a subscription triggers (`presence`), the derivation a view
// re-runs on an event (`receipts`), or the profile cards a view fetches
// (`self`/`contact`). *wa.Client implements it; the fixture and store-free tests
// pass nil when the views they exercise do not touch it.
type DaemonActions interface {
	PresenceActions
	MessageInfoActions
	ContactActions
}

// RegisterDaemonViews registers the daemon-owned views from PROTOCOL.md.
// actions is the client seam some views need; tests and the fixture may pass nil
// when the views they exercise do not touch it.
func RegisterDaemonViews(s *Server, daemon *app.Daemon, store DaemonStore, actions DaemonActions) {
	s.RegisterView("connection", connectionView{daemon: daemon, pending: store})
	s.RegisterView("sync", syncView{daemon: daemon})
	s.RegisterView("login", loginView{daemon: daemon})
	s.RegisterView("chats", chatsView{daemon: daemon, lister: store})
	s.RegisterView("messages", messagesView{daemon: daemon, lister: store})
	s.RegisterView("typing", typingView{daemon: daemon, resolver: store})
	s.RegisterView("presence", presenceView{daemon: daemon, actions: actions})
	s.RegisterView("receipts", receiptsView{daemon: daemon, actions: actions})
	s.RegisterView("self", selfView{daemon: daemon, actions: actions})
	s.RegisterView("contact", contactView{daemon: daemon, actions: actions})
}

type connectionView struct {
	daemon  *app.Daemon
	pending PendingOutgoingCounter
}

func (v connectionView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &connectionSession{eventsCancel: cancel, pending: v.pending, done: make(chan struct{})}
	s.refreshPendingCount()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type connectionSession struct {
	mu           sync.Mutex
	state        app.State
	detail       string
	retryAttempt int32
	nextRetry    int64
	canReconnect bool
	pendingCount int

	pending      PendingOutgoingCounter
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once
}

type connectionItem struct {
	ID                   string `json:"id"`
	State                string `json:"state"`
	Detail               string `json:"detail,omitempty"`
	RetryAttempt         int32  `json:"retry_attempt"`
	NextRetryUnix        int64  `json:"next_retry_unix"`
	CanReconnect         bool   `json:"can_reconnect"`
	PendingOutgoingCount int    `json:"pending_outgoing_count"`
}

func (s *connectionSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *connectionSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.apply(evt) {
				invalidate()
			}
		}
	}
}

func (s *connectionSession) apply(evt app.DaemonEvent) bool {
	s.mu.Lock()
	old := s.itemLocked()
	changed := false
	switch evt.Kind {
	case app.DaemonEventConnectionChanged:
		s.state = evt.State
		s.detail = evt.Detail
		s.retryAttempt = evt.RetryAttempt
		s.nextRetry = evt.NextRetryUnix
		s.canReconnect = evt.CanReconnect
		changed = true
	case app.DaemonEventNewMessage, app.DaemonEventMessageUpdated, app.DaemonEventMessageDeleted, app.DaemonEventChatDeleted, app.DaemonEventChatCleared:
		changed = s.refreshPendingCountLocked()
	}
	newItem := s.itemLocked()
	s.mu.Unlock()
	return changed && old != newItem
}

func (s *connectionSession) refreshPendingCount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshPendingCountLocked()
}

func (s *connectionSession) refreshPendingCountLocked() bool {
	if s.pending == nil {
		return false
	}
	count, err := s.pending.CountPendingOutgoingMessages(context.Background())
	if err != nil {
		log.Printf("protocol: count pending outgoing messages: %v", err)
		return false
	}
	if count == s.pendingCount {
		return false
	}
	s.pendingCount = count
	return true
}

func (s *connectionSession) Items(max int) []Item {
	if max == 0 {
		max = 1
	}
	if max < 1 {
		return nil
	}
	s.mu.Lock()
	item := s.itemLocked()
	s.mu.Unlock()
	return []Item{{ID: "self", Sort: objectViewSort, Data: item}}
}

func (s *connectionSession) itemLocked() connectionItem {
	return connectionItem{
		ID:                   "self",
		State:                stateString(s.state),
		Detail:               s.detail,
		RetryAttempt:         s.retryAttempt,
		NextRetryUnix:        s.nextRetry,
		CanReconnect:         s.canReconnect,
		PendingOutgoingCount: s.pendingCount,
	}
}

func (s *connectionSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

type syncView struct {
	daemon *app.Daemon
}

func (v syncView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &syncSession{eventsCancel: cancel, done: make(chan struct{})}
	s.event = inactiveSyncEvent()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type syncSession struct {
	mu           sync.Mutex
	event        app.HistorySyncEvent
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once
}

type syncItem struct {
	ID                     string `json:"id"`
	Type                   string `json:"type"`
	Phase                  string `json:"phase"`
	ProgressPercent        uint32 `json:"progress_percent"`
	ChunkOrder             uint32 `json:"chunk_order"`
	ConversationsInChunk   uint32 `json:"conversations_in_chunk"`
	MessagesInChunk        uint32 `json:"messages_in_chunk"`
	ProcessedConversations uint32 `json:"processed_conversations"`
	ProcessedMessages      uint32 `json:"processed_messages"`
	IsComplete             bool   `json:"is_complete"`
}

func inactiveSyncEvent() app.HistorySyncEvent {
	return app.HistorySyncEvent{ProgressPercent: 100, IsComplete: true, Phase: app.HistorySyncPhaseComplete}
}

func (s *syncSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *syncSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.apply(evt) {
				invalidate()
			}
		}
	}
}

func (s *syncSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind != app.DaemonEventHistorySyncProgress {
		return false
	}
	s.mu.Lock()
	old := s.itemLocked()
	if evt.HistorySync.IsComplete {
		s.event = evt.HistorySync
		if s.event.Phase == app.HistorySyncPhaseUnspecified {
			s.event.Phase = app.HistorySyncPhaseComplete
		}
		if s.event.ProgressPercent == 0 {
			s.event.ProgressPercent = 100
		}
	} else {
		s.event = evt.HistorySync
	}
	newItem := s.itemLocked()
	s.mu.Unlock()
	return old != newItem
}

func (s *syncSession) Items(max int) []Item {
	if max == 0 {
		max = 1
	}
	if max < 1 {
		return nil
	}
	s.mu.Lock()
	item := s.itemLocked()
	s.mu.Unlock()
	return []Item{{ID: "self", Sort: objectViewSort, Data: item}}
}

func (s *syncSession) itemLocked() syncItem {
	return syncItem{
		ID:                     "self",
		Type:                   historySyncTypeString(s.event.SyncType),
		Phase:                  historySyncPhaseString(s.event.Phase),
		ProgressPercent:        s.event.ProgressPercent,
		ChunkOrder:             s.event.ChunkOrder,
		ConversationsInChunk:   s.event.ConversationsInChunk,
		MessagesInChunk:        s.event.MessagesInChunk,
		ProcessedConversations: s.event.ProcessedConversations,
		ProcessedMessages:      s.event.ProcessedMessages,
		IsComplete:             s.event.IsComplete,
	}
}

func (s *syncSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

func historySyncTypeString(t app.HistorySyncType) string {
	switch t {
	case app.HistorySyncTypeInitialBootstrap:
		return "initial_bootstrap"
	case app.HistorySyncTypeInitialStatusV3:
		return "initial_status_v3"
	case app.HistorySyncTypeFull:
		return "full"
	case app.HistorySyncTypeRecent:
		return "recent"
	case app.HistorySyncTypePushName:
		return "push_name"
	case app.HistorySyncTypeNonBlockingData:
		return "non_blocking_data"
	case app.HistorySyncTypeOnDemand:
		return "on_demand"
	case app.HistorySyncTypeOfflineCatchup:
		return "offline_catchup"
	default:
		return "unspecified"
	}
}

func historySyncPhaseString(phase app.HistorySyncPhase) string {
	switch phase {
	case app.HistorySyncPhaseQueued:
		return "queued"
	case app.HistorySyncPhaseDownloading:
		return "downloading"
	case app.HistorySyncPhaseProcessing:
		return "processing"
	case app.HistorySyncPhaseComplete:
		return "complete"
	case app.HistorySyncPhaseStalled:
		return "stalled"
	default:
		return "unspecified"
	}
}

type loginView struct {
	daemon *app.Daemon
}

func (v loginView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeLoginEvents()
	s := &loginSession{eventsCancel: cancel, done: make(chan struct{}), invalidate: invalidate}
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type loginSession struct {
	mu           sync.Mutex
	state        app.State
	detail       string
	qr           *loginQR
	qrTimer      *time.Timer
	eventsCancel func()
	invalidate   func()
	done         chan struct{}
	closeOnce    sync.Once
}

type loginItem struct {
	ID     string   `json:"id"`
	State  string   `json:"state"`
	Detail string   `json:"detail,omitempty"`
	QR     *loginQR `json:"qr,omitempty"`
}

type loginQR struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *loginSession) drainInitial(events <-chan app.LoginEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *loginSession) run(events <-chan app.LoginEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.apply(evt) {
				invalidate()
			}
		}
	}
}

func (s *loginSession) apply(evt app.LoginEvent) bool {
	s.mu.Lock()
	old := s.itemLocked()
	s.pruneExpiredQRLocked(time.Now())
	switch evt.Kind {
	case app.LoginEventQR:
		if s.state == app.StateNeedLogin {
			s.qr = &loginQR{Code: evt.QRCode, ExpiresAt: evt.ExpiresAt}
			s.scheduleQRExpiryLocked(evt.ExpiresAt)
		}
	default:
		s.state = evt.State
		s.detail = evt.Detail
		if evt.State != app.StateNeedLogin {
			s.clearQRLocked()
		}
	}
	newItem := s.itemLocked()
	s.mu.Unlock()
	return old != newItem
}

func (s *loginSession) Items(max int) []Item {
	if max == 0 {
		max = 1
	}
	if max < 1 {
		return nil
	}
	s.mu.Lock()
	s.pruneExpiredQRLocked(time.Now())
	item := s.itemLocked()
	s.mu.Unlock()
	return []Item{{ID: "self", Sort: objectViewSort, Data: item}}
}

func (s *loginSession) itemLocked() loginItem {
	var qr *loginQR
	if s.qr != nil {
		copy := *s.qr
		qr = &copy
	}
	return loginItem{ID: "self", State: stateString(s.state), Detail: s.detail, QR: qr}
}

func (s *loginSession) scheduleQRExpiryLocked(expiresAt time.Time) {
	if s.qrTimer != nil {
		s.qrTimer.Stop()
		s.qrTimer = nil
	}
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	s.qrTimer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		changed := s.pruneExpiredQRLocked(time.Now())
		s.mu.Unlock()
		if changed {
			s.invalidate()
		}
	})
}

func (s *loginSession) pruneExpiredQRLocked(now time.Time) bool {
	if s.qr == nil || now.Before(s.qr.ExpiresAt) {
		return false
	}
	s.clearQRLocked()
	return true
}

func (s *loginSession) clearQRLocked() {
	if s.qrTimer != nil {
		s.qrTimer.Stop()
		s.qrTimer = nil
	}
	s.qr = nil
}

func (s *loginSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
		s.mu.Lock()
		if s.qrTimer != nil {
			s.qrTimer.Stop()
			s.qrTimer = nil
		}
		s.mu.Unlock()
	})
}
