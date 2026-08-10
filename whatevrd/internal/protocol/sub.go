package protocol

import (
	"bytes"
	"encoding/json"
	"log"
	"reflect"
	"sync"
	"time"
)

// eventEnvelope is the daemon→frontend event shape on the wire for events that
// carry no sort key (reset, remove, ready). Upserts use upsertEnvelope so their
// `sort` is always present on the wire — see emitUpsert.
type eventEnvelope struct {
	Sub       int64           `json:"sub"`
	Event     string          `json:"event"`
	Item      json.RawMessage `json:"item,omitempty"`
	ID        string          `json:"id,omitempty"`
	Exhausted bool            `json:"exhausted,omitempty"`
}

// upsertEnvelope is the wire shape for upserts. `sort` deliberately carries no
// omitempty: the grammar requires every upsert to carry a sort key, so an empty
// key must still be marshalled (and is caught by the assertion in emitUpsert)
// rather than silently dropped into a sort-less, grammar-violating frame.
type upsertEnvelope struct {
	Sub   int64           `json:"sub"`
	Event string          `json:"event"`
	Sort  string          `json:"sort"`
	Item  json.RawMessage `json:"item"`
}

func resetLine(sub int64) []byte {
	line, _ := json.Marshal(eventEnvelope{Sub: sub, Event: "reset"})
	return line
}

// overloadedLine is the terminal frame sent when a connection's outbound queue
// overflows its connection-level cap: an error response the peer may still read,
// after which the connection is closed.
func overloadedLine() []byte {
	line, _ := json.Marshal(response{ID: nullID, Error: errorf(CodeInternal, "connection outbound queue overflowed; closing")})
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
// change detection during recompute. value is the unmarshalled source of body,
// kept so an unchanged item can be recognised without re-marshalling it.
type sentItem struct {
	sort string
	body []byte
	// value is the Item.Data body was produced from.
	value any
}

// same reports whether data would marshal to the same body. View items are
// plain structs holding scalars, slices and pointers to more of the same, so a
// deep comparison is both correct and far cheaper than marshalling: it
// allocates nothing and stops at the first difference. `==` is not an option —
// items carrying slices (reactions, mentions) would panic.
func (s sentItem) same(data any) bool {
	return s.value != nil && reflect.DeepEqual(s.value, data)
}

// subscription keeps one client's copy of one view window correct: it
// re-reads the session's items whenever the view invalidates it, diffs
// against what the client already holds, and emits upserts/removes (and
// `ready` after subscribe/extend fills).
type subscription struct {
	id   int64
	sink frameSink
	sess ViewSession
	// dir is sess when it manages its own two-frontier window (anchored
	// messages); nil for a plain prefix session. Set once at subscribe.
	dir DirectionalSession

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

// validateExtend rejects a direction the window's shape cannot honor. Only a
// prefix (live-edge) session is constrained: its newer edge is the live edge,
// so `newer` is meaningless there. A directional session accepts both
// directions (nothing-left-to-reach is a no-op, surfaced via Exhausted).
func (s *subscription) validateExtend(direction string) *Error {
	if s.dir == nil && direction == "newer" {
		return errorf(CodeInvalidParams, "cannot extend this window newer; its newer edge is the live edge, where items arrive on their own")
	}
	return nil
}

// applyExtend grows the window and schedules a fill that ends in `ready`. Like
// start, the caller enqueues the extend response first. Readies coalesce
// across rapid extends; PROTOCOL.md defines ready as covering the *latest*
// subscribe/extend, so clients may not count them. A directional session owns
// its window, so growth routes to ExtendWindow; a prefix session grows its
// single-integer window here (only `older` reaches this path — `newer` was
// rejected in validateExtend).
func (s *subscription) applyExtend(direction string, count int) {
	if s.dir != nil {
		s.dir.ExtendWindow(direction, count)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == nil && s.window > 0 {
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
	start := time.Now()
	var items []Item
	defer func() { logRecompute(s.id, len(items), start) }()
	if s.dir != nil {
		// The session owns its window: Items(0) is the whole current window and
		// exhaustion is per the frontier last extended. No prefix trim.
		items = s.sess.Items(0)
		exhausted = s.dir.Exhausted()
	} else {
		fetch := 0
		if window > 0 {
			fetch = window + 1
		}
		items = s.sess.Items(fetch)
		exhausted = true
		if window > 0 && len(items) > window {
			items = items[:window]
			exhausted = false
		}
	}

	next := make(map[string]sentItem, len(items))
	for _, it := range items {
		if _, dup := next[it.ID]; dup {
			log.Printf("protocol: view session yielded duplicate item id %q (sub %d)", it.ID, s.id)
			continue
		}
		// Most recomputes change nothing: an unrelated avatar landing re-reads
		// the whole window only to find every row identical. Marshalling each
		// one just to byte-compare it made that no-op cost proportional to the
		// window (a 1024-member roster re-marshalled per avatar event), so skip
		// the marshal entirely when the source value is unchanged.
		prev, hadPrev := s.sent[it.ID]
		if hadPrev && prev.sort == it.Sort && prev.same(it.Data) {
			next[it.ID] = prev
			continue
		}
		body, err := json.Marshal(it.Data)
		if err != nil {
			log.Printf("protocol: marshal view item %q (sub %d): %v", it.ID, s.id, err)
			continue
		}
		cur := sentItem{sort: it.Sort, body: body, value: it.Data}
		next[it.ID] = cur
		if hadPrev && prev.sort == it.Sort && bytes.Equal(prev.body, body) {
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
	if sort == "" {
		// A sort-less upsert is a grammar violation the frontend engine cannot
		// place. It means a view returned an Item with an empty Sort — a daemon
		// bug. Log it loudly; the id-as-sort fallback keeps the frame well-formed
		// so one buggy view cannot desync the whole subscription.
		log.Printf("protocol: view emitted upsert with empty sort (sub=%d id=%q); falling back to id", s.id, id)
		sort = id
	}
	line, _ := json.Marshal(upsertEnvelope{Sub: s.id, Event: "upsert", Sort: sort, Item: body})
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
