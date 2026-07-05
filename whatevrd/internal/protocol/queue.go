package protocol

import (
	"container/list"
	"sync"
)

// maxQueuedEventsPerSub bounds how many event frames may sit in a
// connection's outbound queue for one subscription. Because upserts and
// removes coalesce by item id, a healthy consumer's queue stays around the
// window size; blowing past this bound means the consumer is not draining,
// so the queue for that subscription is dropped and the client is resynced
// with `reset`. A var, not a const, so tests can lower it.
var maxQueuedEventsPerSub = 8192

// frameKey identifies the queued event frame a newer event for the same
// item supersedes.
type frameKey struct {
	sub int64
	id  string
}

// queuedFrame is one marshaled line awaiting the write loop. closeAfter
// tears the connection down once the line is flushed (hello rejection).
// sub is 0 for connection-level frames (responses), which are never
// coalesced or purged; key is the item id for coalescible event frames.
type queuedFrame struct {
	line       []byte
	closeAfter bool
	sub        int64
	key        string
}

// outQueue is a connection's outbound frame queue. It preserves FIFO order
// (responses enqueued before events are written before them), coalesces
// event frames by (sub, item id) — only the latest version of an item
// matters — and falls back to purge + `reset` for a subscription whose
// queue overflows.
type outQueue struct {
	mu     sync.Mutex
	frames list.List
	byKey  map[frameKey]*list.Element
	counts map[int64]int
	live   map[int64]bool

	// signal wakes the write loop; buffered so enqueuing never blocks.
	signal chan struct{}
}

func newOutQueue() *outQueue {
	return &outQueue{
		byKey:  map[frameKey]*list.Element{},
		counts: map[int64]int{},
		live:   map[int64]bool{},
		signal: make(chan struct{}, 1),
	}
}

// push enqueues a connection-level frame (response or hello rejection).
func (q *outQueue) push(line []byte, closeAfter bool) {
	q.mu.Lock()
	q.frames.PushBack(queuedFrame{line: line, closeAfter: closeAfter})
	q.mu.Unlock()
	q.wake()
}

// pushEvent enqueues an event frame for a live subscription, superseding
// any queued frame with the same non-empty key. It reports whether the
// subscription overflowed and was replaced by a `reset`; the caller must
// then rebuild the client copy from scratch.
func (q *outQueue) pushEvent(sub int64, key string, line []byte) (reset bool) {
	q.mu.Lock()
	if !q.live[sub] {
		q.mu.Unlock()
		return false
	}
	if key != "" {
		if el, ok := q.byKey[frameKey{sub, key}]; ok {
			q.removeLocked(el)
		}
	}
	el := q.frames.PushBack(queuedFrame{line: line, sub: sub, key: key})
	if key != "" {
		q.byKey[frameKey{sub, key}] = el
	}
	q.counts[sub]++
	if q.counts[sub] > maxQueuedEventsPerSub {
		q.purgeLocked(sub)
		q.frames.PushBack(queuedFrame{line: resetLine(sub), sub: sub})
		q.counts[sub] = 1
		reset = true
	}
	q.mu.Unlock()
	q.wake()
	return reset
}

// addSub admits a subscription's events into the queue.
func (q *outQueue) addSub(sub int64) {
	q.mu.Lock()
	q.live[sub] = true
	q.mu.Unlock()
}

// closeSub purges every queued frame of a subscription and refuses its
// future events, so nothing for it hits the wire after the unsubscribe
// response.
func (q *outQueue) closeSub(sub int64) {
	q.mu.Lock()
	delete(q.live, sub)
	q.purgeLocked(sub)
	q.mu.Unlock()
}

// pop removes and returns the head frame; ok is false when the queue is
// empty (wait on signal, then retry).
func (q *outQueue) pop() (frame queuedFrame, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	el := q.frames.Front()
	if el == nil {
		return queuedFrame{}, false
	}
	frame = q.removeLocked(el)
	return frame, true
}

func (q *outQueue) removeLocked(el *list.Element) queuedFrame {
	frame := q.frames.Remove(el).(queuedFrame)
	if frame.key != "" {
		k := frameKey{frame.sub, frame.key}
		if q.byKey[k] == el {
			delete(q.byKey, k)
		}
	}
	if frame.sub != 0 {
		if q.counts[frame.sub]--; q.counts[frame.sub] <= 0 {
			delete(q.counts, frame.sub)
		}
	}
	return frame
}

func (q *outQueue) purgeLocked(sub int64) {
	var next *list.Element
	for el := q.frames.Front(); el != nil; el = next {
		next = el.Next()
		if el.Value.(queuedFrame).sub == sub {
			q.removeLocked(el)
		}
	}
}

func (q *outQueue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}
