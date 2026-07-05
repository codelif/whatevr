package protocol

import (
	"bytes"
	"testing"
)

func popAll(q *outQueue) []queuedFrame {
	var frames []queuedFrame
	for {
		frame, ok := q.pop()
		if !ok {
			return frames
		}
		frames = append(frames, frame)
	}
}

func TestQueuePreservesFIFOAcrossKinds(t *testing.T) {
	q := newOutQueue()
	q.addSub(1)

	q.push([]byte(`{"id":1,"result":{}}`), false)
	q.pushEvent(1, "a", []byte(`upsert-a`))
	q.pushEvent(1, "", []byte(`ready`))

	frames := popAll(q)
	if len(frames) != 3 {
		t.Fatalf("queued %d frames, want 3", len(frames))
	}
	for i, want := range []string{`{"id":1,"result":{}}`, `upsert-a`, `ready`} {
		if string(frames[i].line) != want {
			t.Errorf("frame %d = %q, want %q", i, frames[i].line, want)
		}
	}
}

func TestQueueCoalescesSameItem(t *testing.T) {
	q := newOutQueue()
	q.addSub(1)

	q.pushEvent(1, "a", []byte(`upsert-a-v1`))
	q.pushEvent(1, "b", []byte(`upsert-b`))
	q.pushEvent(1, "a", []byte(`upsert-a-v2`))

	frames := popAll(q)
	if len(frames) != 2 {
		t.Fatalf("queued %d frames, want 2 (a coalesced)", len(frames))
	}
	// Only the latest version of `a` survives, superseding its old slot.
	if string(frames[0].line) != `upsert-b` || string(frames[1].line) != `upsert-a-v2` {
		t.Fatalf("frames = %q, %q; want upsert-b, upsert-a-v2", frames[0].line, frames[1].line)
	}
}

func TestQueueRemoveSupersedesQueuedUpsert(t *testing.T) {
	q := newOutQueue()
	q.addSub(1)

	q.pushEvent(1, "a", []byte(`upsert-a`))
	q.pushEvent(1, "a", []byte(`remove-a`))

	frames := popAll(q)
	if len(frames) != 1 || string(frames[0].line) != `remove-a` {
		t.Fatalf("frames = %v, want just remove-a", frames)
	}
}

func TestQueueCoalescingIsPerSub(t *testing.T) {
	q := newOutQueue()
	q.addSub(1)
	q.addSub(2)

	q.pushEvent(1, "a", []byte(`sub1-a`))
	q.pushEvent(2, "a", []byte(`sub2-a`))

	if frames := popAll(q); len(frames) != 2 {
		t.Fatalf("same item id on different subs must not coalesce; got %d frames", len(frames))
	}
}

func TestQueueOverflowPurgesAndResets(t *testing.T) {
	old := maxQueuedEventsPerSub
	maxQueuedEventsPerSub = 3
	defer func() { maxQueuedEventsPerSub = old }()

	q := newOutQueue()
	q.addSub(1)
	q.push([]byte(`response`), false)

	var reset bool
	for i, key := range []string{"a", "b", "c", "d"} {
		if q.pushEvent(1, key, []byte("upsert-"+key)) {
			if i != 3 {
				t.Fatalf("reset fired at frame %d, want 3", i)
			}
			reset = true
		}
	}
	if !reset {
		t.Fatalf("overflow did not report reset")
	}

	frames := popAll(q)
	if len(frames) != 2 {
		t.Fatalf("queued %d frames after overflow, want 2 (response + reset)", len(frames))
	}
	if string(frames[0].line) != `response` {
		t.Fatalf("connection-level frame was purged: %q", frames[0].line)
	}
	if !bytes.Contains(frames[1].line, []byte(`"event":"reset"`)) {
		t.Fatalf("frame after purge = %q, want reset event", frames[1].line)
	}
}

func TestQueueOverflowLeavesOtherSubsAlone(t *testing.T) {
	old := maxQueuedEventsPerSub
	maxQueuedEventsPerSub = 2
	defer func() { maxQueuedEventsPerSub = old }()

	q := newOutQueue()
	q.addSub(1)
	q.addSub(2)
	q.pushEvent(2, "x", []byte(`sub2-x`))
	q.pushEvent(1, "a", []byte(`sub1-a`))
	q.pushEvent(1, "b", []byte(`sub1-b`))
	if !q.pushEvent(1, "c", []byte(`sub1-c`)) {
		t.Fatalf("expected sub 1 to overflow")
	}

	frames := popAll(q)
	if len(frames) != 2 {
		t.Fatalf("queued %d frames, want 2 (sub2-x + sub1 reset)", len(frames))
	}
	if string(frames[0].line) != `sub2-x` {
		t.Fatalf("innocent sub's frame lost: %q", frames[0].line)
	}
}

func TestQueueCloseSubPurgesAndBlocks(t *testing.T) {
	q := newOutQueue()
	q.addSub(1)
	q.pushEvent(1, "a", []byte(`upsert-a`))
	q.pushEvent(1, "", []byte(`ready`))

	q.closeSub(1)
	if q.pushEvent(1, "b", []byte(`upsert-b`)) {
		t.Fatalf("push to closed sub reported reset")
	}

	if frames := popAll(q); len(frames) != 0 {
		t.Fatalf("closed sub left %d frames queued", len(frames))
	}
}

func TestQueuePushToUnknownSubIsDropped(t *testing.T) {
	q := newOutQueue()
	q.pushEvent(9, "a", []byte(`upsert-a`))
	if frames := popAll(q); len(frames) != 0 {
		t.Fatalf("event for unregistered sub was queued")
	}
}

func TestQueueCoalescingAfterPop(t *testing.T) {
	q := newOutQueue()
	q.addSub(1)

	q.pushEvent(1, "a", []byte(`upsert-a-v1`))
	if _, ok := q.pop(); !ok {
		t.Fatalf("pop failed")
	}
	// v1 already left the queue; v2 must enqueue fresh, not vanish into a
	// stale byKey entry.
	q.pushEvent(1, "a", []byte(`upsert-a-v2`))
	frames := popAll(q)
	if len(frames) != 1 || string(frames[0].line) != `upsert-a-v2` {
		t.Fatalf("frames = %v, want just upsert-a-v2", frames)
	}
}
