package views

import (
	"context"
	"math"
	"sync"
	"sync/atomic"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"

	"whatevrd/internal/core"
	"whatevrd/internal/model"
	"whatevrd/internal/server"
)

// place is a messages window's Position. counting walks the chat's whole
// index, so it reruns only when a change could have moved the count (rows
// of the chat came or went, the chat folded) or the window starts on
// another row.
type place struct {
	stale atomic.Bool
	// edge is the live edge, whose offset is the count less what was kept
	edge bool

	mu      sync.Mutex
	pos     server.Position
	first   model.Cursor
	counted bool
}

func (p *place) moved(c core.Change, addrs map[string]bool) {
	if touchesAny(c, "message", addrs) || touchesAny(c, "chat", addrs) {
		p.stale.Store(true)
	}
}

// count brings the position up to a window whose oldest row is first, none
// when has is false.
func (p *place) count(ctx context.Context, r *model.Reader, addrs []string, first model.Cursor, has bool) error {
	stale := p.stale.Swap(false)
	p.mu.Lock()
	counted, was, pos := p.counted, p.first, p.pos
	p.mu.Unlock()
	if counted && !stale && (p.edge || first == was) {
		return nil
	}
	if stale || !counted {
		n, err := r.MessageCount(ctx, addrs, nil)
		if err != nil {
			p.stale.Store(true)
			return err
		}
		pos.Count = uint64(n)
	}
	pos.Offset = 0
	if !p.edge && has {
		n, err := r.MessageCount(ctx, addrs, &first)
		if err != nil {
			p.stale.Store(true)
			return err
		}
		pos.Offset = uint64(n)
	}
	p.mu.Lock()
	p.pos, p.first, p.counted = pos, first, true
	p.mu.Unlock()
	return nil
}

func (p *place) Position(kept int) server.Position {
	p.mu.Lock()
	defer p.mu.Unlock()
	pos := p.pos
	if p.edge {
		pos.Offset = pos.Count - min(uint64(kept), pos.Count)
	}
	return pos
}

// placed is a window with a place.
type placed struct {
	*win
	*place
}

// chatList is the chat list's Position: the count is a seek on the list's
// own index, cheap enough for every pass.
type chatList struct {
	mu  sync.Mutex
	pos server.Position
}

func (l *chatList) set(count, offset int) {
	l.mu.Lock()
	l.pos = server.Position{Count: uint64(count), Offset: uint64(offset)}
	l.mu.Unlock()
}

func (l *chatList) Position(int) server.Position {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pos
}

// chatsTop is the chat list from the top, a live-edge window.
type chatsTop struct {
	merging
	*chatList
}

// chatRange is a run of the chat list by position: rows start to
// start+size in the order the list has now. it keeps its place while chats
// move through it, and grows from either end.
type chatRange struct {
	merging
	*chatList

	mu          sync.Mutex
	start, size int
	lastDir     v2.Direction
	// what the last read found past each end
	above, below bool
}

func (r *chatRange) Extend(d v2.Direction, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d == v2.Direction_DIRECTION_NEWER {
		up := min(count, r.start)
		r.start -= up
		r.size += up
	} else {
		r.size += count
	}
	r.lastDir = d
}

func (r *chatRange) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

func (r *chatRange) Exhausted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.lastDir {
	case v2.Direction_DIRECTION_OLDER:
		return !r.below
	case v2.Direction_DIRECTION_NEWER:
		return !r.above
	}
	return !r.above && !r.below
}

// span is the rows to read for a list of count, the start clamped so a
// range past the end still shows its last size rows.
func (r *chatRange) span(count int) (start, size int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.start = max(min(r.start, count-r.size), 0)
	return r.start, r.size
}

func (r *chatRange) read(start, got, count int) {
	r.mu.Lock()
	r.above, r.below = start > 0, start+got < count
	r.mu.Unlock()
}

// around is where a window of size centred on index starts.
func around(index uint64, size int) int {
	return max(int(min(index, math.MaxInt32))-(size-1)/2, 0)
}

// chatSortAt is the list position a chat row's sort says, see chatItem.
func chatSortAt(b []byte) (pinned bool, pinT, lastT int64, key string, ok bool) {
	if len(b) < 17 || b[0] > 1 {
		return false, 0, 0, "", false
	}
	pinned = b[0] == 0
	pinT = math.MaxInt64 - unasc(b[1:9])
	lastT = math.MaxInt64 - unasc(b[9:17])
	return pinned, pinT, lastT, string(b[17:]), true
}
