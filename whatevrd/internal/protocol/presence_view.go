package protocol

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"whatevrd/internal/app"
)

// PresenceActions is the upstream side of the `presence` view. Availability is
// delivered by WhatsApp only on request (unlike typing, which arrives
// unsolicited — which is why the two are separate views), so *subscribing* to a
// chat's presence is what asks WhatsApp to start sending it. *wa.Client
// implements it; SubscribeChatPresence is a no-op for group chats (WhatsApp
// broadcasts availability for individuals only), so a group presence view simply
// stays empty.
type PresenceActions interface {
	SubscribeChatPresence(ctx context.Context, chatID string) error
}

// presenceView is the per-chat availability view: one item per participant
// (jid, availability, last_seen). Today the daemon tracks a single availability
// slot per chat — for a direct chat that is the one counterpart, whose jid is
// the chat id — so the view carries at most one item; the item is keyed by the
// participant jid to stay forward-compatible if group availability ever lands.
//
// Availability rides in on the SenderID-empty half of the overloaded
// DaemonEventChatPresence; the SenderID-set (composing) half belongs to the
// `typing` view. Subscribing drives the upstream WhatsApp presence subscription
// (PresenceActions); the cached availability, if any, seeds the initial fill.
type presenceView struct {
	daemon  *app.Daemon
	actions PresenceActions
}

type presenceParams struct {
	ChatID string `json:"chat_id"`
}

func (v presenceView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p presenceParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed presence params")
		}
	}
	if p.ChatID == "" {
		return nil, nil, errorf(CodeInvalidParams, "presence params must carry a chat_id")
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &presenceSession{
		daemon:       v.daemon,
		actions:      v.actions,
		chatID:       p.ChatID,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
		wasOnline:    true,
	}
	// Seed the initial fill from any availability the daemon already cached for
	// this chat (from an earlier subscription in this daemon session), then drain
	// events buffered since SubscribeDaemonEvents. The two may overlap, but the
	// state write is idempotent so a doubly-applied event is harmless.
	s.reloadAvailability()
	s.drainInitial(events)
	// Subscribing is the trigger: ask WhatsApp to start delivering availability
	// for this chat. Results arrive later as ordinary DaemonEventChatPresence
	// availability events on our subscription. Groups are a no-op upstream.
	if v.actions != nil {
		if err := v.actions.SubscribeChatPresence(ctx, p.ChatID); err != nil {
			log.Printf("protocol: subscribe chat presence %s: %v", p.ChatID, err)
		}
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

// presenceSession tracks the availability of the chat's single participant.
type presenceSession struct {
	daemon       *app.Daemon
	actions      PresenceActions
	chatID       string
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once

	// wasOnline tracks the last connection state seen on this session so a renewal
	// fires only on a real offline→online transition, not on the ConnectionChanged
	// state SubscribeDaemonEvents replays at open (Open already subscribes once).
	// Initialised true because that open-time subscribe stands in for the first
	// online edge; only a subsequent reconnect needs to re-issue it.
	wasOnline bool

	mu           sync.Mutex
	hasData      bool
	availability app.ContactAvailability
	lastSeen     int64
}

// reloadAvailability seeds/refreshes the held availability from the daemon's
// cached snapshot. It is the initial fill and the resync recovery after a
// dropped-event gap; if the daemon has no cached availability yet it leaves the
// current state untouched (nothing authoritative to replace it with).
func (s *presenceSession) reloadAvailability() {
	avail, lastSeen, ok := s.daemon.ChatAvailability(s.chatID)
	if !ok {
		return
	}
	s.mu.Lock()
	s.hasData = true
	s.availability = avail
	s.lastSeen = lastSeen
	s.mu.Unlock()
}

type presenceItem struct {
	ID           string `json:"id"` // participant jid (== chat_id for a direct chat)
	Availability string `json:"availability"`
	LastSeenUnix int64  `json:"last_seen_unix,omitempty"`
}

func (s *presenceSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *presenceSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

// apply folds one daemon event into the availability state, reporting whether it
// changed. Availability events are the SenderID-empty half of the overloaded
// chat-presence event; a set SenderID means composing, which belongs to the
// `typing` view and is ignored here.
func (s *presenceSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind == app.DaemonEventResync {
		s.reloadAvailability()
		return true
	}
	if evt.Kind == app.DaemonEventConnectionChanged {
		// Re-issue the upstream presence subscription on a real offline→online
		// transition: a WhatsApp presence subscription does not survive a reconnect
		// (and a subscribe attempted while offline at Open failed), so without this
		// the view would stop updating after any disconnect. Fire only on the edge —
		// SubscribeDaemonEvents replays the current state as a ConnectionChanged at
		// open, and Open already subscribed once, so re-subscribing on that replay
		// would be a redundant duplicate. WhatsApp has no presence *unsubscribe*
		// primitive, so teardown on the last local unsubscribe is a no-op — the
		// demand-driven half that matters is (re)subscribing on demand.
		online := evt.State == app.StateOnline
		reconnected := online && !s.wasOnline
		s.wasOnline = online
		if reconnected && s.actions != nil {
			if err := s.actions.SubscribeChatPresence(s.ctx, s.chatID); err != nil {
				log.Printf("protocol: renew chat presence %s: %v", s.chatID, err)
			}
		}
		return false
	}
	if evt.Kind != app.DaemonEventChatPresence || evt.SenderID != "" {
		return false
	}
	if evt.Chat.ID != s.chatID {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasData && s.availability == evt.Availability && s.lastSeen == evt.LastSeenUnix {
		return false
	}
	s.hasData = true
	s.availability = evt.Availability
	s.lastSeen = evt.LastSeenUnix
	return true
}

// Items reports the participant's availability, or nothing until availability
// has been observed. The view holds at most one item, so the window (max) never
// bites; the sort key is the participant jid (order is immaterial, a stable key
// keeps upserts idempotent).
func (s *presenceSession) Items(_ int) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasData {
		return nil
	}
	return []Item{{ID: s.chatID, Sort: s.chatID, Data: s.itemLocked()}}
}

func (s *presenceSession) itemLocked() presenceItem {
	// last_seen is only meaningful while offline; an online contact's event
	// carries last_seen 0, and the frontend renders "online" regardless.
	lastSeen := int64(0)
	if s.availability == app.ContactAvailabilityOffline {
		lastSeen = s.lastSeen
	}
	return presenceItem{
		ID:           s.chatID,
		Availability: availabilityString(s.availability),
		LastSeenUnix: lastSeen,
	}
}

func (s *presenceSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

func availabilityString(a app.ContactAvailability) string {
	switch a {
	case app.ContactAvailabilityOnline:
		return "online"
	case app.ContactAvailabilityOffline:
		return "offline"
	default:
		return "unspecified"
	}
}
