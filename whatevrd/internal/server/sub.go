package server

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"

	"whatevrd/internal/core"
)

// View opens subscriptions of one Subscribe arm.
type View interface {
	Open(ctx context.Context, s *Session, req *v2.Subscribe) (Window, *v2.SubscribeResult, error)
}

// Window is one subscription's handle on a view. Items is the first max
// items in view order, all of them when max is 0; an error means no answer,
// never no items, and the engine keeps what it sent. Wake says a change may
// have moved what Items returns.
type Window interface {
	Items(ctx context.Context, max int) ([]*v2.Upsert, error)
	Wake(c core.Change) bool
	Close()
}

// Bounded is a window around a point in history with two frontiers of its
// own, a messages view anchored at unread or a message. it owns its size:
// Items(0) is all of it, never more than the view's cap.
type Bounded interface {
	Window
	Extend(d v2.Direction, count int)
	// Exhausted is whether the frontier last extended has nothing further
	Exhausted() bool
	// Size is how many items the window reaches for now
	Size() int
}

// Position is where a window sits in its whole view: how many items the view
// has and how many sort before the window's first.
type Position struct{ Count, Offset uint64 }

// Positioned is a window that knows its Position as of its last Items, of
// which the engine kept the first kept.
type Positioned interface {
	Position(kept int) Position
}

// Sized is a window with a size of its own for a subscribe that gave none.
type Sized interface {
	DefaultLimit() int
}

// Merging is a window whose ids can fold into another, chats and people:
// ReplacedBy is the id a removed one folded into, "" for none.
type Merging interface {
	ReplacedBy(id string) string
}

type sent struct {
	sort string
	sum  [sha256.Size]byte
}

// subscription keeps one frontend's copy of one window equal to it: each
// pass reads the window, compares it with what was sent and queues the
// difference as one update.
type subscription struct {
	id     uint64
	conn   *conn
	win    Window
	bnd    Bounded
	log    zerolog.Logger
	params *v2.Subscribe
	// the view's caps, see view
	itemBytes, cap int

	mu        sync.Mutex
	limit     int
	ready     bool
	dirty     bool
	running   bool
	started   bool
	closed    bool
	resetting bool

	// only the running pass touches these
	sent    map[string]sent
	sentPos *Position
}

func (s *subscription) kick() {
	s.mu.Lock()
	s.dirty = true
	s.runLocked()
	s.mu.Unlock()
}

// start begins delivery once the subscribe response is queued, so the fill
// comes after it.
func (s *subscription) start() {
	s.mu.Lock()
	s.started, s.dirty, s.ready = true, true, true
	s.runLocked()
	s.mu.Unlock()
}

func (s *subscription) extend(d v2.Direction, count int) {
	if s.bnd != nil {
		s.bnd.Extend(d, count)
	}
	s.mu.Lock()
	if s.bnd == nil && s.limit > 0 {
		s.limit += count
	}
	s.ready, s.dirty = true, true
	s.runLocked()
	s.mu.Unlock()
}

// size is how many items the window reaches for.
func (s *subscription) size() int {
	if s.bnd != nil {
		return s.bnd.Size()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit
}

func (s *subscription) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.win.Close()
}

func (s *subscription) runLocked() {
	if !s.started || s.running || s.closed {
		return
	}
	s.running = true
	go s.run()
}

func (s *subscription) run() {
	s.mu.Lock()
	for s.dirty && !s.closed {
		s.dirty = false
		limit, ready, reset := s.limit, s.ready, s.resetting
		s.ready, s.resetting = false, false
		s.mu.Unlock()

		ok := s.pass(limit, ready, reset)

		s.mu.Lock()
		if !ok {
			// the queue dropped this subscription's updates: start over
			s.sent, s.sentPos = nil, nil
			s.dirty, s.ready, s.resetting = true, true, true
		}
	}
	s.running = false
	s.mu.Unlock()
}

var marshal = proto.MarshalOptions{Deterministic: true}

// pass reads the window and queues what changed. false means the queue
// overflowed and the whole window must go again under a reset.
func (s *subscription) pass(limit int, ready, reset bool) bool {
	start := time.Now()
	if s.conn.ctx.Err() != nil {
		// the connection is gone
		return true
	}
	// not cancellable: on a cancellable ctx database/sql starts a goroutine
	// per query, and a pass runs hundreds. on one cpu those starve the
	// socket reader. the read is capped, so a dead connection's finishes
	// and is dropped
	ctx := s.log.WithContext(context.WithoutCancel(s.conn.ctx))
	fetch := 0
	if s.bnd == nil && limit > 0 {
		fetch = limit + 1
	}
	items, err := s.win.Items(ctx, fetch)
	if s.conn.ctx.Err() != nil {
		return true
	}
	if err != nil {
		s.log.Warn().Err(err).Int("kept", len(s.sent)).Msg("keeping what was sent after a failed read")
		if ready {
			// still answer the subscribe or extend, with what the client has
			return s.conn.q.pushUpdate(&update{sub: s.id, ready: true})
		}
		return true
	}
	exhausted := true
	if s.bnd != nil {
		exhausted = s.bnd.Exhausted()
	} else if limit > 0 && len(items) > limit {
		items, exhausted = items[:limit], false
	}
	u := &update{sub: s.id, reset: reset, changes: map[string][]byte{}, ready: ready, exhausted: exhausted}
	next := make(map[string]sent, len(items))
	for _, it := range items {
		id := it.GetId()
		if _, dup := next[id]; dup {
			s.log.Error().Str("item", id).Msg("a view gave one id twice")
			continue
		}
		if len(it.GetSort()) == 0 {
			s.log.Error().Str("item", id).Msg("a view gave an item no sort, using its id")
			it.SetSort([]byte(id))
		}
		b, err := marshal.Marshal(it)
		if err != nil {
			s.log.Error().Err(err).Str("item", id).Msg("marshal an item")
			continue
		}
		if len(b) > s.itemBytes && s.conn.srv.Fit != nil && s.conn.srv.Fit(it, s.itemBytes) {
			if b, err = marshal.Marshal(it); err != nil {
				s.log.Error().Err(err).Str("item", id).Msg("marshal an item")
				continue
			}
		}
		if len(b) > s.itemBytes {
			// the window must still fit a frame
			s.log.Error().Str("item", id).Int("bytes", len(b)).Int("max", s.itemBytes).Msg("a view gave an item over its size")
			continue
		}
		cur := sent{sort: string(it.GetSort()), sum: sha256.Sum256(b)}
		next[id] = cur
		if prev, had := s.sent[id]; had && prev == cur {
			continue
		}
		u.order = append(u.order, id)
		u.changes[id] = upsertChange(b)
	}
	var m Merging
	if x, ok := s.win.(Merging); ok {
		m = x
	}
	for id := range s.sent {
		if _, ok := next[id]; ok {
			continue
		}
		by := ""
		if m != nil {
			by = m.ReplacedBy(id)
		}
		u.order = append(u.order, id)
		u.changes[id] = removeChange(id, by)
	}
	s.sent = next
	if p, ok := s.win.(Positioned); ok {
		if pos := p.Position(len(items)); reset || s.sentPos == nil || *s.sentPos != pos {
			u.pos, s.sentPos = &pos, &pos
		}
	}
	if !reset && u.bytes() > maxFrameBytes {
		// more change than one frame holds: send the window whole instead
		return false
	}
	if len(u.order) > 0 || ready || reset || u.pos != nil {
		if !s.conn.q.pushUpdate(u) {
			return false
		}
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		s.log.Info().Dur("dur", d).Int("items", len(items)).Int("changes", len(u.order)).Msg("slow view pass")
	}
	return true
}
