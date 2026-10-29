package server

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"

	"whatevrd/internal/core"
)

// posList is a fakeList that says its place: count is set by hand, the
// offset is what the window kept short of it
type posList struct {
	fakeList
	count atomic.Uint64
}

type posWin struct {
	listWin
	l *posList
}

func (l *posList) Open(context.Context, *Session, *v2.Subscribe) (Window, *v2.SubscribeResult, error) {
	return posWin{listWin{&l.fakeList}, l}, nil, nil
}

func (w posWin) Position(kept int) Position {
	n := w.l.count.Load()
	return Position{Count: n, Offset: n - min(uint64(kept), n)}
}

func TestPositionsGoOutWhenTheyMove(t *testing.T) {
	l := &posList{}
	l.set("a", "b", "c")
	l.count.Store(10)
	s, c := start(t, l)
	c.hello()
	subscribe(c, 2)
	u := c.update()
	if !u.HasPosition() || u.GetPosition().GetCount() != 10 || u.GetPosition().GetOffset() != 8 {
		t.Fatalf("first %v", u)
	}
	// rows changed, the place did not: no position
	l.set("a", "x", "c")
	s.Changed(core.Change{Keys: map[string][]string{"chat": {"x"}}})
	u = c.update()
	if up, _ := ids(u); !slices.Contains(up, "x") || u.HasPosition() {
		t.Fatalf("change %v", u)
	}
	// only the place moved: an update with nothing else
	l.count.Store(12)
	s.Changed(core.Change{Keys: map[string][]string{"chat": {"x"}}})
	u = c.update()
	if len(u.GetChanges()) > 0 || u.GetReset() || u.GetPosition().GetCount() != 12 || u.GetPosition().GetOffset() != 10 {
		t.Fatalf("moved %v", u)
	}
	// an offset of 0 still arrives as a position
	l.count.Store(2)
	s.Changed(core.Change{Keys: map[string][]string{"chat": {"x"}}})
	u = c.update()
	if !u.HasPosition() || u.GetPosition().GetCount() != 2 || u.GetPosition().GetOffset() != 0 {
		t.Fatalf("top %v", u)
	}
}

func TestHelloListsPositions(t *testing.T) {
	_, c := start(t, &fakeList{})
	c.send(func(r *v2.Request) { r.SetHello(v2.Hello_builder{Client: "t", Protocol: 2}.Build()) })
	if f := c.read().GetResponse().GetHello().GetFeatures(); !slices.Contains(f, "positions") {
		t.Fatalf("features %v", f)
	}
}
