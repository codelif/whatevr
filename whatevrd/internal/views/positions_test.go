package views

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"

	"whatevrd/internal/core"
)

// update is the next whole update of sub, others skipped.
func (f *fixture) update(sub uint64) *v2.ViewUpdate {
	f.t.Helper()
	u := f.read().GetEvent().GetUpdate()
	for u != nil && u.GetSub() != sub {
		u = f.read().GetEvent().GetUpdate()
	}
	if u == nil {
		f.t.Fatal("not an update")
	}
	return u
}

func (f *fixture) open(set func(*v2.Subscribe)) uint64 {
	f.t.Helper()
	f.send(func(r *v2.Request) {
		s := &v2.Subscribe{}
		set(s)
		r.SetSubscribe(s)
	})
	// updates of the other subs can come first
	fr := f.read()
	for !fr.HasResponse() {
		fr = f.read()
	}
	r := fr.GetResponse()
	if e := r.GetError(); e != nil {
		f.t.Fatalf("subscribe: %v", e)
	}
	return r.GetSubscribe().GetSub()
}

func sits(t *testing.T, what string, u *v2.ViewUpdate, count, offset uint64) {
	t.Helper()
	if !u.HasPosition() || u.GetPosition().GetCount() != count || u.GetPosition().GetOffset() != offset {
		t.Fatalf("%s: want %d at %d, got %v", what, count, offset, u)
	}
}

func texts(u *v2.ViewUpdate) string {
	var out []string
	for _, c := range u.GetChanges() {
		if c.HasUpsert() {
			out = append(out, c.GetUpsert().GetMessage().GetText())
		}
	}
	return strings.Join(out, "")
}

func TestMessagesSayWhereTheySit(t *testing.T) {
	var ins []core.Input
	for i, id := range strings.Split("ABCDEFGHIJ", "") {
		ins = append(ins, msg(id, ashaPN, ashaPN, "", false, i+1, id))
	}
	f := open(t, ins...)
	_, chats := f.subscribe(func(s *v2.Subscribe) { s.SetChats(&v2.ChatsView{}) })
	chat := chats[0].GetId()

	live := f.open(func(s *v2.Subscribe) {
		s.SetLimit(4)
		s.SetMessages(v2.MessagesView_builder{ChatId: chat}.Build())
	})
	u := f.update(live)
	sits(t, "live edge", u, 10, 6)
	f.feed(msg("K", ashaPN, ashaPN, "", false, 20, "K"))
	sits(t, "a new one", f.update(live), 11, 7)

	at := f.open(func(s *v2.Subscribe) {
		s.SetLimit(3)
		s.SetMessages(v2.MessagesView_builder{ChatId: chat, Index: proto.Uint64(2)}.Build())
	})
	u = f.update(at)
	if got := texts(u); got != "BCD" {
		t.Fatalf("index 2 %q", got)
	}
	sits(t, "index 2", u, 11, 1)
	// past the end clamps to the newest
	far := f.open(func(s *v2.Subscribe) {
		s.SetLimit(3)
		s.SetMessages(v2.MessagesView_builder{ChatId: chat, Index: proto.Uint64(1 << 40)}.Build())
	})
	u = f.update(far)
	if got := texts(u); !strings.HasSuffix(got, "K") {
		t.Fatalf("clamped %q", got)
	}

	// one going away before the window moves its offset and the count
	f.feed(deleteForMe("A", 30))
	f.until(t, "after a delete", map[uint64][2]uint64{
		live: {10, 6},
		at:   {10, 0},
	})
}

// until reads updates until every sub in want was last placed at its
// count and offset.
func (f *fixture) until(t *testing.T, what string, want map[uint64][2]uint64) {
	t.Helper()
	got := map[uint64]*v2.Position{}
	for {
		done := true
		for sub, w := range want {
			p := got[sub]
			done = done && p != nil && p.GetCount() == w[0] && p.GetOffset() == w[1]
		}
		if done {
			return
		}
		fr := f.read()
		if u := fr.GetEvent().GetUpdate(); u.HasPosition() {
			got[u.GetSub()] = u.GetPosition()
		}
		if fr.HasResponse() {
			t.Fatalf("%s: a response, places %v", what, got)
		}
	}
}

func TestChatsPageByPosition(t *testing.T) {
	var ins []core.Input
	for i := range 8 {
		pn := fmt.Sprintf("9177700001%02d@s.whatsapp.net", i)
		ins = append(ins, msg(fmt.Sprint("C", i), pn, pn, "", false, 100-i, fmt.Sprint(i)))
	}
	f := open(t, ins...)
	top := f.open(func(s *v2.Subscribe) {
		s.SetLimit(3)
		s.SetChats(&v2.ChatsView{})
	})
	u := f.update(top)
	sits(t, "top", u, 8, 0)
	var sorts [][]byte
	for _, c := range u.GetChanges() {
		sorts = append(sorts, c.GetUpsert().GetSort())
	}

	mid := f.open(func(s *v2.Subscribe) {
		s.SetLimit(2)
		s.SetChats(v2.ChatsView_builder{Index: proto.Uint64(4)}.Build())
	})
	u = f.update(mid)
	sits(t, "index 4", u, 8, 4)
	if len(u.GetChanges()) != 2 || u.GetReady().GetExhausted() {
		t.Fatalf("index 4 %v", u)
	}
	f.send(func(r *v2.Request) {
		r.SetExtend(v2.Extend_builder{Sub: mid, Count: 2, Direction: v2.Direction_DIRECTION_NEWER}.Build())
	})
	if !f.read().GetResponse().HasDone() {
		t.Fatal("extend newer")
	}
	sits(t, "up two", f.update(mid), 8, 2)
	f.send(func(r *v2.Request) {
		r.SetExtend(v2.Extend_builder{Sub: mid, Count: 10, Direction: v2.Direction_DIRECTION_OLDER}.Build())
	})
	if !f.read().GetResponse().HasDone() {
		t.Fatal("extend older")
	}
	u = f.update(mid)
	if !u.GetReady().GetExhausted() {
		t.Fatalf("down to the end %v", u)
	}

	// a sort anchor lands on its row, a chat moving above shifts the offset
	at := f.open(func(s *v2.Subscribe) {
		s.SetLimit(1)
		s.SetChats(v2.ChatsView_builder{Sort: sorts[2]}.Build())
	})
	sits(t, "sort", f.update(at), 8, 2)
	f.feed(msg("NEW", "917770000999@s.whatsapp.net", "917770000999@s.whatsapp.net", "", false, 200, "new"))
	f.until(t, "a chat above", map[uint64][2]uint64{
		top: {9, 0},
		at:  {9, 2},
	})
}
