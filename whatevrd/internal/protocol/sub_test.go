package protocol

import (
	"strings"
	"sync"
	"testing"
)

// fakeSink records a subscription's emitted lines and can simulate the
// outbound queue's overflow behavior (purge + reset) on the nth event.
type fakeSink struct {
	mu      sync.Mutex
	lines   []string
	resetAt int // 1-based event index that triggers a reset; 0 = never
	n       int
}

func (f *fakeSink) sendEvent(sub int64, key string, line []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.resetAt != 0 && f.n == f.resetAt {
		// The real queue purges this sub's frames and enqueues `reset`;
		// the marker stands in for that reset frame.
		f.lines = append(f.lines, "RESET")
		return true
	}
	f.lines = append(f.lines, string(line))
	return false
}

func (f *fakeSink) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

// startFakeSub wires a subscription straight to a fakeSink, bypassing the
// socket, exactly as handleSubscribe would.
func startFakeSub(t *testing.T, view *memView, sink *fakeSink, window int) *subscription {
	t.Helper()
	sub := newSubscription(1, sink, window)
	sess, _, err := view.Open(nil, sub.kick)
	if err != nil {
		t.Fatalf("open view: %v", err)
	}
	sub.sess = sess
	t.Cleanup(sub.close)
	sub.start()
	return sub
}

func kinds(lines []string) string {
	var out []string
	for _, l := range lines {
		switch {
		case l == "RESET":
			out = append(out, "RESET")
		case strings.Contains(l, `"event":"upsert"`):
			out = append(out, "upsert")
		case strings.Contains(l, `"event":"remove"`):
			out = append(out, "remove")
		case strings.Contains(l, `"event":"ready"`):
			out = append(out, "ready")
		default:
			out = append(out, "?")
		}
	}
	return strings.Join(out, ",")
}

func TestSubscriptionResetDuringFillRefills(t *testing.T) {
	view := newMemView()
	view.put("a", "1", nil)
	view.put("b", "2", nil)

	sink := &fakeSink{resetAt: 1} // the very first upsert overflows
	startFakeSub(t, view, sink, 0)

	// After the reset the engine must forget what it sent and deliver a
	// complete fresh fill closed by ready.
	waitFor(t, "refill after reset", func() bool {
		return kinds(sink.snapshot()) == "RESET,upsert,upsert,ready"
	})
}

func TestSubscriptionResetOnLiveUpdateRefillsWithReady(t *testing.T) {
	view := newMemView()
	view.put("a", "1", nil)
	view.put("b", "2", nil)

	sink := &fakeSink{}
	startFakeSub(t, view, sink, 0)
	waitFor(t, "initial fill", func() bool {
		return kinds(sink.snapshot()) == "upsert,upsert,ready"
	})

	// The 4th event (the live upsert of c) overflows: even though no
	// subscribe/extend is pending, the resync must end in ready per the
	// reset contract ("fresh upserts follow, then ready").
	sink.mu.Lock()
	sink.resetAt = 4
	sink.mu.Unlock()
	view.put("c", "3", nil)

	waitFor(t, "resync after live-update reset", func() bool {
		return kinds(sink.snapshot()) == "upsert,upsert,ready,RESET,upsert,upsert,upsert,ready"
	})

	// All three items were re-sent in the resync.
	lines := sink.snapshot()
	resync := lines[4:7]
	for i, id := range []string{"a", "b", "c"} {
		if !strings.Contains(resync[i], `"id":"`+id+`"`) {
			t.Fatalf("resync upsert %d = %s, want item %s", i, resync[i], id)
		}
	}
}

// F15: a view that returns an Item with an empty Sort must still yield a
// well-formed upsert — the engine falls back to the item id rather than emitting
// a sort-less, grammar-violating frame.
func TestSubscriptionEmptySortFallsBackToID(t *testing.T) {
	view := newMemView()
	sink := &fakeSink{}
	view.put("a", "", map[string]any{"v": 1}) // empty sort key
	startFakeSub(t, view, sink, 0)
	waitFor(t, "upsert then ready", func() bool {
		return kinds(sink.snapshot()) == "upsert,ready"
	})
	upsert := sink.snapshot()[0]
	if !strings.Contains(upsert, `"sort":"a"`) {
		t.Fatalf("empty-sort upsert should carry id as sort, got %s", upsert)
	}
}

func TestSubscriptionCoalescesKicksIntoOneRecompute(t *testing.T) {
	view := newMemView()
	sink := &fakeSink{}
	startFakeSub(t, view, sink, 0)
	waitFor(t, "empty fill", func() bool {
		return kinds(sink.snapshot()) == "ready"
	})

	// Many rewrites of the same item may collapse into fewer recomputes,
	// but the final state must be the last version, exactly once per
	// recompute that saw it changed.
	for i := range 50 {
		view.put("a", "1", map[string]any{"v": i})
	}
	waitFor(t, "last version delivered", func() bool {
		lines := sink.snapshot()
		return len(lines) > 1 && strings.Contains(lines[len(lines)-1], `"v":49`)
	})
	for _, l := range sink.snapshot()[1:] {
		if !strings.Contains(l, `"event":"upsert"`) || !strings.Contains(l, `"id":"a"`) {
			t.Fatalf("unexpected event during rewrite storm: %s", l)
		}
	}
}
