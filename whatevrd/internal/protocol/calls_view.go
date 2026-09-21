package protocol

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"whatevrd/internal/app"
)

// callsView is the global "what is ringing right now" collection: one item
// per locally-ringing call (id = call id). Items appear on offer and are
// `remove`d on terminate/reject/answer-elsewhere. It is unwindowed and tiny.
// The desktop cannot answer — whatsmeow has no media stack — so the item is
// what a frontend needs to render "incoming call, answer on your phone" with
// a Reject button wired to `call.reject`.
type callsView struct {
	daemon   *app.Daemon
	resolver SenderDisplayer
}

func (v callsView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &callsSession{
		daemon:       v.daemon,
		resolver:     v.resolver,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
		ringing:      make(map[string]app.RingingCall),
	}
	s.reloadRinging()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type callsSession struct {
	daemon       *app.Daemon
	resolver     SenderDisplayer
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once

	mu      sync.Mutex
	ringing map[string]app.RingingCall // call_id -> call
}

// reloadRinging rebuilds the ringing set from the daemon snapshot: the
// initial fill and the resync recovery.
func (s *callsSession) reloadRinging() {
	fresh := make(map[string]app.RingingCall)
	for _, call := range s.daemon.RingingCalls() {
		fresh[call.CallID] = call
	}
	s.mu.Lock()
	s.ringing = fresh
	s.mu.Unlock()
}

type callsItem struct {
	ID        string        `json:"id"` // call_id
	ChatID    string        `json:"chat_id"`
	Caller    messageSender `json:"caller"`
	Video     bool          `json:"video,omitempty"`
	StartedAt int64         `json:"started_at"`
}

func (s *callsSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *callsSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

// apply folds one daemon event into the ringing set. Call events carry only
// identity, so any change re-reads the authoritative snapshot — the set is
// tiny and churn is rare.
func (s *callsSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind != app.DaemonEventCallChanged && evt.Kind != app.DaemonEventResync {
		return false
	}
	before := len(s.ringing)
	s.reloadRinging()
	return len(s.ringing) != before || evt.Kind == app.DaemonEventCallChanged
}

// Items reports ringing calls oldest first (the one ringing longest sorts
// first); frontends never look inside the sort key.
func (s *callsSession) Items(max int) []Item {
	s.mu.Lock()
	calls := make([]app.RingingCall, 0, len(s.ringing))
	for _, call := range s.ringing {
		calls = append(calls, call)
	}
	s.mu.Unlock()

	sort.Slice(calls, func(i, j int) bool {
		if calls[i].StartedUnix != calls[j].StartedUnix {
			return calls[i].StartedUnix < calls[j].StartedUnix
		}
		return calls[i].CallID < calls[j].CallID
	})
	if max > 0 && len(calls) > max {
		calls = calls[:max]
	}

	ctx := s.ctx
	items := make([]Item, 0, len(calls))
	for _, call := range calls {
		name := ""
		if s.resolver != nil && call.CallerID != "" {
			if n, _, err := s.resolver.SenderDisplay(ctx, call.CallerID); err == nil {
				name = n
			}
		}
		items = append(items, Item{
			ID:   call.CallID,
			Sort: call.CallID,
			Data: callsItem{
				ID:        call.CallID,
				ChatID:    call.ChatID,
				Caller:    messageSender{ID: call.CallerID, Name: name},
				Video:     call.Video,
				StartedAt: call.StartedUnix,
			},
		})
	}
	return items
}

func (s *callsSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}
