package protocol

import (
	"bytes"
	"encoding/json"
	"log"
	"sync"
)

// eventEnvelope is the daemon→frontend event shape on the wire.
type eventEnvelope struct {
	Sub       int64           `json:"sub"`
	Event     string          `json:"event"`
	Sort      string          `json:"sort,omitempty"`
	Item      json.RawMessage `json:"item,omitempty"`
	ID        string          `json:"id,omitempty"`
	Exhausted bool            `json:"exhausted,omitempty"`
}

func resetLine(sub int64) []byte {
	line, _ := json.Marshal(eventEnvelope{Sub: sub, Event: "reset"})
	return line
}

// frameSink is where a subscription's event frames go; *conn implements it
// on top of its outbound queue. The returned reset reports that the sink
// overflowed, dropped this subscription's queue, and already emitted
// `reset` — the caller must rebuild from scratch.
type frameSink interface {
	sendEvent(sub int64, itemKey string, line []byte) (reset bool)
}

// sentItem records the version of an item the client last received, for
// change detection during recompute.
type sentItem struct {
	sort string
	body []byte
}

// subscription keeps one client's copy of one view window correct: it
// re-reads the session's items whenever the view invalidates it, diffs
// against what the client already holds, and emits upserts/removes (and
// `ready` after subscribe/extend fills).
type subscription struct {
	id   int64
	sink frameSink
	sess ViewSession

	mu           sync.Mutex
	window       int // 0 = unbounded
	pendingReady bool
	dirty        bool
	running      bool
	started      bool
	closed       bool

	// sent maps item id → last emitted version. Only the run goroutine
	// touches it (the running flag guarantees a single runner).
	sent map[string]sentItem
}

func newSubscription(id int64, sink frameSink, window int) *subscription {
	return &subscription{id: id, sink: sink, window: window}
}

// kick schedules a window recomputation; views call it (via the invalidate
// callback) whenever their contents may have changed. Safe from any
// goroutine; cheap when a recomputation is already pending.
func (s *subscription) kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	s.maybeRunLocked()
}

// start begins delivery: the initial fill runs asynchronously, so calling
// start only after the subscribe response is enqueued guarantees the wire
// order response → upserts → ready. Before start, kicks accumulate into
// the first fill instead of emitting.
func (s *subscription) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	s.dirty = true
	s.pendingReady = true
	s.maybeRunLocked()
}

// extend grows the window and schedules a fill that ends in `ready`. Like
// start, the caller enqueues the extend response first. Readies coalesce
// across rapid extends; PROTOCOL.md defines ready as covering the *latest*
// subscribe/extend, so clients may not count them.
func (s *subscription) extend(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window > 0 {
		s.window += count
	}
	s.pendingReady = true
	s.dirty = true
	s.maybeRunLocked()
}

// close ends delivery and releases the view session. Idempotent; safe from
// any goroutine. The session is closed outside s.mu so a view holding its
// own lock in Close can never deadlock against a concurrent invalidate.
func (s *subscription) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	sess := s.sess
	s.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
}

func (s *subscription) maybeRunLocked() {
	if !s.started || s.running || s.closed {
		return
	}
	s.running = true
	go s.run()
}

// run drains dirtiness: each pass recomputes the window once, then loops if
// more kicks arrived meanwhile. A sink reset wipes the sent map so the next
// pass re-emits the whole window and closes with ready, exactly the
// "fresh upserts follow, then ready" contract of `reset`.
func (s *subscription) run() {
	s.mu.Lock()
	for s.dirty && !s.closed {
		s.dirty = false
		window := s.window
		ready := s.pendingReady
		s.pendingReady = false
		s.mu.Unlock()

		exhausted, reset := s.recompute(window)
		if !reset && ready {
			reset = s.emitReady(exhausted)
		}

		s.mu.Lock()
		if reset {
			s.sent = nil
			s.dirty = true
			s.pendingReady = true
		}
	}
	s.running = false
	s.mu.Unlock()
}

// recompute pulls the current window from the session and emits the
// difference against what the client holds. Fetching one item beyond the
// window tells us whether there is anything left to extend into.
func (s *subscription) recompute(window int) (exhausted, reset bool) {
	fetch := 0
	if window > 0 {
		fetch = window + 1
	}
	items := s.sess.Items(fetch)
	exhausted = true
	if window > 0 && len(items) > window {
		items = items[:window]
		exhausted = false
	}

	next := make(map[string]sentItem, len(items))
	for _, it := range items {
		if _, dup := next[it.ID]; dup {
			log.Printf("protocol: view session yielded duplicate item id %q (sub %d)", it.ID, s.id)
			continue
		}
		body, err := json.Marshal(it.Data)
		if err != nil {
			log.Printf("protocol: marshal view item %q (sub %d): %v", it.ID, s.id, err)
			continue
		}
		next[it.ID] = sentItem{sort: it.Sort, body: body}
		if prev, ok := s.sent[it.ID]; ok && prev.sort == it.Sort && bytes.Equal(prev.body, body) {
			continue
		}
		if s.emitUpsert(it.ID, it.Sort, body) {
			return exhausted, true
		}
	}
	for id := range s.sent {
		if _, ok := next[id]; ok {
			continue
		}
		if s.emitRemove(id) {
			return exhausted, true
		}
	}
	s.sent = next
	return exhausted, false
}

func (s *subscription) emitUpsert(id, sort string, body []byte) (reset bool) {
	line, _ := json.Marshal(eventEnvelope{Sub: s.id, Event: "upsert", Sort: sort, Item: body})
	return s.sink.sendEvent(s.id, id, line)
}

func (s *subscription) emitRemove(id string) (reset bool) {
	line, _ := json.Marshal(eventEnvelope{Sub: s.id, Event: "remove", ID: id})
	return s.sink.sendEvent(s.id, id, line)
}

func (s *subscription) emitReady(exhausted bool) (reset bool) {
	line, _ := json.Marshal(eventEnvelope{Sub: s.id, Event: "ready", Exhausted: exhausted})
	return s.sink.sendEvent(s.id, "", line)
}
