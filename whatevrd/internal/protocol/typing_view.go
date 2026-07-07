package protocol

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"whatevrd/internal/app"
)

// SenderDisplayer resolves a sender jid to its stored display name (and avatar,
// unused here). *store.DB implements it; the `typing` view uses it to label the
// composing sender.
type SenderDisplayer interface {
	SenderDisplay(ctx context.Context, id string) (name, avatarPath string, err error)
}

// typingView is the global "who is composing right now" collection. It carries
// one item per chat with an active composing sender (id = chat_id); the item's
// `senders` list is the localizable material a frontend needs, and the whole
// item is `remove`d when composing stops. It is unwindowed and tiny — chat
// lists and conversation headers both read it, everyone else skips it.
//
// The daemon tracks a single composing sender per chat (presence is a single
// slot), so `senders` currently holds at most one entry; modelling it as a list
// keeps the wire shape forward-compatible if that ever grows.
type typingView struct {
	daemon   *app.Daemon
	resolver SenderDisplayer
}

func (v typingView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &typingSession{
		daemon:       v.daemon,
		resolver:     v.resolver,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
		composing:    make(map[string]string),
	}
	// Snapshot the current composing set for the initial fill, then drain any
	// events buffered since SubscribeDaemonEvents. The two may overlap, but the
	// map writes are idempotent so a doubly-applied event is harmless.
	s.reloadComposing()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

// typingSession tracks, per chat, the jid of the sender currently composing.
// It mirrors the daemon's single-slot presence model: one sender per chat,
// cleared as soon as composing stops.
type typingSession struct {
	daemon       *app.Daemon
	resolver     SenderDisplayer
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once

	mu        sync.Mutex
	composing map[string]string // chat_id -> composing sender jid
}

// reloadComposing rebuilds the composing set from the daemon snapshot. It is the
// initial fill and the resync recovery: after a dropped-event gap the folded map
// cannot be trusted, so it is replaced wholesale with the authoritative snapshot.
func (s *typingSession) reloadComposing() {
	fresh := make(map[string]string)
	for _, cc := range s.daemon.ComposingChats() {
		fresh[cc.ChatID] = cc.SenderID
	}
	s.mu.Lock()
	s.composing = fresh
	s.mu.Unlock()
}

type typingItem struct {
	ID      string         `json:"id"` // chat_id
	Senders []typingSender `json:"senders"`
}

type typingSender struct {
	JID  string `json:"jid"`
	Name string `json:"name,omitempty"`
}

func (s *typingSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *typingSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

// apply folds one daemon event into the composing set, reporting whether the
// set changed. Chat-presence events are overloaded: composing events always
// carry a SenderID, availability events never do — so a missing SenderID is the
// discriminator that keeps availability churn from touching the typing view.
func (s *typingSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind == app.DaemonEventResync {
		s.reloadComposing()
		return true
	}
	// A composing sender's name/avatar can change while the indicator is open;
	// Items re-resolves the display name per call, so re-emit on those refreshes
	// (the engine's content diff drops it if nothing a row shows actually changed).
	if evt.Kind == app.DaemonEventAvatarUpdated || evt.Kind == app.DaemonEventContactInfoUpdated {
		s.mu.Lock()
		composing := len(s.composing) > 0
		s.mu.Unlock()
		return composing
	}
	if evt.Kind != app.DaemonEventChatPresence || evt.SenderID == "" {
		return false
	}
	chatID := evt.Chat.ID
	if chatID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if evt.IsComposing {
		if s.composing[chatID] == evt.SenderID {
			return false
		}
		s.composing[chatID] = evt.SenderID
		return true
	}
	if _, ok := s.composing[chatID]; !ok {
		return false
	}
	delete(s.composing, chatID)
	return true
}

// Items reports the composing chats in chat-id order (order is immaterial for
// this unwindowed view, but a stable deterministic sort keeps upserts idempotent
// and tests simple). The sort key is the chat id; frontends never look inside it.
func (s *typingSession) Items(max int) []Item {
	s.mu.Lock()
	pairs := make([][2]string, 0, len(s.composing))
	for chatID, senderID := range s.composing {
		pairs = append(pairs, [2]string{chatID, senderID})
	}
	s.mu.Unlock()

	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	if max > 0 && len(pairs) > max {
		pairs = pairs[:max]
	}

	ctx := s.ctx
	items := make([]Item, 0, len(pairs))
	for _, p := range pairs {
		chatID, senderID := p[0], p[1]
		name := ""
		if s.resolver != nil {
			if n, _, err := s.resolver.SenderDisplay(ctx, senderID); err == nil {
				name = n
			}
		}
		items = append(items, Item{
			ID:   chatID,
			Sort: chatID,
			Data: typingItem{ID: chatID, Senders: []typingSender{{JID: senderID, Name: name}}},
		})
	}
	return items
}

func (s *typingSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}
