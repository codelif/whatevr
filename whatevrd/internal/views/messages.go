package views

import (
	"context"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"

	"whatevrd/internal/core"
	"whatevrd/internal/live"
	"whatevrd/internal/model"
	"whatevrd/internal/server"
)

// messagesLimit is a messages window's size when the subscribe gave none
const messagesLimit = 50

// MessageCap is the most messages one window holds
var MessageCap = server.WindowCap(messageBytes)

// anchoredCap is MessageCap, apart from in tests
var anchoredCap = MessageCap

// msgSort is where a message sits: by time, then arrival, then whatsapp's
// id, as the model pages them.
func msgSort(m model.Message) []byte { return append(asc(asc(nil, m.T), m.Ord), m.ID...) }

// sortAt is the position a msgSort says.
func sortAt(b []byte) (model.Cursor, bool) {
	if len(b) <= 16 {
		return model.Cursor{}, false
	}
	return model.Cursor{T: unasc(b[:8]), Ord: unasc(b[8:16]), ID: string(b[16:])}, true
}

func cursorOf(m model.Message) model.Cursor { return model.Cursor{T: m.T, Ord: m.Ord, ID: m.ID} }

// messageItems is ms as items, ids and avatars landing at finish.
func (c *rc) messageItems(ch chatCtx, ms []model.Message) []*v2.Upsert {
	rows := c.messages(ch, ms)
	out := make([]*v2.Upsert, len(rows))
	for i, r := range rows {
		it := &v2.Upsert{}
		it.SetId(r.GetId())
		it.SetSort(msgSort(ms[i]))
		it.SetMessage(r)
		out[i] = it
	}
	return out
}

// watch is what a window of message rows wakes on: its chat's addresses
// and the people its rows named, both as of the last read.
type watch struct {
	addrs atomic.Pointer[map[string]bool]
	shown atomic.Pointer[map[string]bool]
}

func (w *watch) saw(ch model.Chat, c *rc) {
	a := set(ch.Addrs...)
	a[ch.Key] = true
	w.addrs.Store(&a)
	shown := c.shown
	if shown == nil {
		shown = map[string]bool{}
	}
	w.shown.Store(&shown)
}

// moved marks pl stale when c could have moved its count.
func (w *watch) moved(c core.Change, pl *place) {
	if a := w.addrs.Load(); a != nil {
		pl.moved(c, *a)
	}
}

func (w *watch) wake(c core.Change) bool {
	if c.All["person"] || touches(c, "sticker") {
		return true
	}
	if a := w.addrs.Load(); a != nil {
		for _, k := range []string{"message", "chat", "live", live.TouchTransfer, "person"} {
			if touchesAny(c, k, *a) {
				return true
			}
		}
	}
	if s := w.shown.Load(); s != nil && touchesAny(c, "person", *s) {
		return true
	}
	return false
}

func (rs *Reads) messagesView(ctx context.Context, s *server.Session, req *v2.Subscribe) (server.Window, *v2.SubscribeResult, error) {
	p := req.GetMessages()
	id := p.GetChatId()
	ch, err := rs.chatByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	switch p.WhichAnchor() {
	case v2.MessagesView_Unread_case:
		anchor, ok, err := rs.unreadAnchor(ctx, ch)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			// nothing unread is the live edge, as if latest were asked
			return rs.latest(req, id, ch), nil, nil
		}
		a := rs.anchored(req, id, ch, cursorOf(anchor))
		res := &v2.SubscribeResult{}
		res.SetAnchorId(Token(anchor))
		return a, res, nil
	case v2.MessagesView_MessageId_case:
		addr, wa, ok := SplitToken(p.GetMessageId())
		if !ok {
			return nil, nil, invalid("malformed message id %q", p.GetMessageId())
		}
		addrs := ch.Addrs
		if !slices.Contains(addrs, addr) {
			return nil, nil, notFound("no message %q in chat %q", p.GetMessageId(), id)
		}
		m, ok, err := rs.r.Message(ctx, addrs, wa)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, notFound("no message %q in chat %q", p.GetMessageId(), id)
		}
		// a picture inside an album shows inside it
		if m.Album != "" {
			if parent, ok, err := rs.r.Message(ctx, addrs, m.Album); err == nil && ok {
				m = parent
			}
		}
		return rs.anchored(req, id, ch, cursorOf(m)), nil, nil
	case v2.MessagesView_Sort_case:
		at, ok := sortAt(p.GetSort())
		if !ok {
			return nil, nil, invalid("malformed sort")
		}
		return rs.anchored(req, id, ch, at), nil, nil
	case v2.MessagesView_Index_case:
		total, err := rs.r.MessageCount(ctx, ch.Addrs, nil)
		if err != nil {
			return nil, nil, err
		}
		at, ok, err := rs.r.MessageAt(ctx, ch.Addrs, int(min(p.GetIndex(), math.MaxInt32)), total)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			return rs.anchored(req, id, ch, at), nil, nil
		}
	}
	return rs.latest(req, id, ch), nil, nil
}

// unreadAnchor is the oldest unread message, counting back the chat's unread
// count over what others sent.
func (rs *Reads) unreadAnchor(ctx context.Context, ch model.Chat) (model.Message, bool, error) {
	if ch.Unread <= 0 {
		return model.Message{}, false, nil
	}
	var anchor model.Message
	found := false
	from := model.Cursor{}
	seen := 0
	for seen < ch.Unread {
		page, err := rs.r.Messages(ctx, ch.Addrs, from, 200, false)
		if err != nil {
			return model.Message{}, false, err
		}
		if len(page) == 0 {
			break
		}
		for _, m := range page {
			from = model.Cursor{T: m.T, Ord: m.Ord, ID: m.ID}
			if m.FromMe || m.Facts.Revoked || strings.HasPrefix(m.Kind, "stub:") {
				continue
			}
			anchor, found = m, true
			if seen++; seen == ch.Unread {
				break
			}
		}
	}
	return anchor, found, nil
}

// latest is the live edge: the newest messages, newest first.
func (rs *Reads) latest(req *v2.Subscribe, id string, ch model.Chat) server.Window {
	wt := &watch{}
	wt.saw(ch, &rc{})
	pl := &place{edge: true}
	w := &win{params: req, limit: messagesLimit, wake: func(c core.Change) bool {
		wt.moved(c, pl)
		return wt.wake(c)
	}}
	w.items = func(ctx context.Context, max int) ([]*v2.Upsert, error) {
		c, err := rs.begin(ctx)
		if err != nil {
			return nil, err
		}
		ch, ok, err := c.chat(id)
		if err != nil || !ok {
			return nil, err
		}
		ms, err := rs.r.Messages(ctx, ch.Addrs, model.Cursor{}, max, false)
		if err != nil {
			return nil, err
		}
		if err := pl.count(ctx, rs.r, ch.Addrs, model.Cursor{}, false); err != nil {
			return nil, err
		}
		out := c.messageItems(chatOf(ch), ms)
		if err := c.finish(); err != nil {
			return nil, err
		}
		wt.saw(ch, c)
		return out, nil
	}
	return placed{w, pl}
}

// anchoredWin is a window around one message with two frontiers of its own.
type anchoredWin struct {
	rs     *Reads
	params *v2.Subscribe
	chatID string
	watch
	place

	mu sync.Mutex
	// at is the anchor's position, its id found under any of the chat's
	// addresses. a window whose anchor is gone stays at the position
	at             model.Cursor
	older, newer   int
	lastDir        v2.Direction
	olderExhausted bool
	newerExhausted bool
	// live latches once the newer frontier reaches the newest message: from
	// then on what arrives is next to the window and belongs in it. before,
	// a message past the frontier would sit behind a gap.
	live bool
}

func (rs *Reads) anchored(req *v2.Subscribe, id string, ch model.Chat, at model.Cursor) *anchoredWin {
	total := int(req.GetLimit())
	if total <= 0 {
		total = messagesLimit
	}
	half := (total - 1) / 2
	a := &anchoredWin{rs: rs, params: req, chatID: id, at: at, older: half, newer: total - 1 - half}
	a.saw(ch, &rc{})
	return a
}

func (a *anchoredWin) Params() *v2.Subscribe { return a.params }
func (a *anchoredWin) Close()                {}

func (a *anchoredWin) Wake(c core.Change) bool {
	a.watch.moved(c, &a.place)
	return a.wake(c)
}

func (a *anchoredWin) Extend(d v2.Direction, count int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d == v2.Direction_DIRECTION_NEWER {
		a.newer += count
	} else {
		a.older += count
	}
	a.lastDir = d
}

// Size is the window's reach: the anchor and both sides.
func (a *anchoredWin) Size() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.older + 1 + a.newer
}

func (a *anchoredWin) Exhausted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.lastDir {
	case v2.Direction_DIRECTION_OLDER:
		return a.olderExhausted
	case v2.Direction_DIRECTION_NEWER:
		return a.newerExhausted
	}
	return a.olderExhausted && a.newerExhausted
}

func (a *anchoredWin) Items(ctx context.Context, _ int) ([]*v2.Upsert, error) {
	a.mu.Lock()
	olderN, newerN, live, at := a.older, a.newer, a.live, a.at
	a.mu.Unlock()
	c, err := a.rs.begin(ctx)
	if err != nil {
		return nil, err
	}
	ch, ok, err := c.chat(a.chatID)
	if err != nil || !ok {
		return nil, err
	}
	anchor, ok, err := a.rs.r.Message(ctx, ch.Addrs, at.ID)
	if err != nil {
		return nil, err
	}
	var mid []model.Message
	if ok {
		at, mid = cursorOf(anchor), []model.Message{anchor}
	}
	older, err := a.rs.r.Messages(ctx, ch.Addrs, at, olderN+1, false)
	if err != nil {
		return nil, err
	}
	olderExhausted := len(older) <= olderN
	older = older[:min(len(older), olderN)]
	newerLimit := newerN + 1
	if live {
		newerLimit = anchoredCap
	}
	newer, err := a.rs.r.Messages(ctx, ch.Addrs, at, newerLimit, true)
	if err != nil {
		return nil, err
	}
	newerExhausted := live || len(newer) <= newerN
	if !live {
		newer = newer[:min(len(newer), newerN)]
	}
	slices.Reverse(older)
	window := slices.Concat(older, mid, newer)
	// at the live edge new messages grow the window: past the cap the oldest
	// go, and the anchor moves up to the oldest kept
	moved := false
	if live && len(newer) >= anchoredCap {
		// a window's worth came at once: the window is the newest of them
		latest, err := a.rs.r.Messages(ctx, ch.Addrs, model.Cursor{}, anchoredCap, false)
		if err != nil {
			return nil, err
		}
		if len(latest) > 0 {
			slices.Reverse(latest)
			window, moved = latest, true
		}
	} else if drop := len(window) - anchoredCap; drop > 0 {
		window, moved = window[drop:], true
	}
	if moved {
		older, olderExhausted = nil, false
		at, newer = cursorOf(window[0]), window[1:]
	}

	a.mu.Lock()
	a.olderExhausted, a.newerExhausted = olderExhausted, newerExhausted
	a.at = at
	switch {
	case moved:
		a.older, a.newer = 0, len(newer)
		a.live = true
	case newerExhausted:
		a.live = true
		// the reach says what is held, so a later extend never asks for less
		a.newer = max(a.newer, len(newer))
	}
	a.mu.Unlock()

	var first model.Cursor
	if len(window) > 0 {
		first = cursorOf(window[0])
	}
	if err := a.count(ctx, a.rs.r, ch.Addrs, first, len(window) > 0); err != nil {
		return nil, err
	}
	out := c.messageItems(chatOf(ch), window)
	if err := c.finish(); err != nil {
		return nil, err
	}
	a.saw(ch, c)
	return out, nil
}
