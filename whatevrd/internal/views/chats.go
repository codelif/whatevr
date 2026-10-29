package views

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow/types"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"

	"whatevrd/internal/core"
	"whatevrd/internal/live"
	"whatevrd/internal/model"
	"whatevrd/internal/server"
)

// what a chat row is read from
var chatKinds = []string{"chat", "chatrow", "message", "person", "group", TouchClock, TouchSends, live.TouchOlder}

func (rs *Reads) chatsView(ctx context.Context, s *server.Session, req *v2.Subscribe) (server.Window, *v2.SubscribeResult, error) {
	p := req.GetChats()
	f := model.ChatFilter{Archived: p.GetArchived()}
	switch p.GetFilter() {
	case v2.ChatFilter_CHAT_FILTER_DIRECT:
		f.Kind = "direct"
	case v2.ChatFilter_CHAT_FILTER_GROUPS:
		f.Kind = "groups"
	}
	w := &win{params: req}
	list := &chatList{}
	// rows from start, max of them (all for 0)
	read := func(ctx context.Context, start, max int) ([]*v2.Upsert, error) {
		gen := rs.previews.begin()
		c, err := rs.begin(ctx)
		if err != nil {
			return nil, err
		}
		f := f
		f.Limit, f.Offset = max, start
		chats, err := rs.r.ChatsIn(ctx, c.w, f)
		if err != nil {
			return nil, err
		}
		out := make([]*v2.Upsert, len(chats))
		for i, ch := range chats {
			out[i] = c.chatItem(gen, ch)
		}
		return out, c.finish()
	}
	w.wake = func(c core.Change) bool { return touches(c, chatKinds...) }
	w.replaced = rs.replaced

	var index uint64
	switch p.WhichAnchor() {
	case v2.ChatsView_Index_case:
		index = p.GetIndex()
	case v2.ChatsView_Sort_case:
		pinned, pinT, lastT, key, ok := chatSortAt(p.GetSort())
		if !ok {
			return nil, nil, invalid("malformed sort")
		}
		n, err := rs.r.ChatsBefore(ctx, f, pinned, pinT, lastT, key)
		if err != nil {
			return nil, nil, err
		}
		index = uint64(n)
	default:
		w.items = func(ctx context.Context, max int) ([]*v2.Upsert, error) {
			count, err := rs.r.ChatCount(ctx, f)
			if err != nil {
				return nil, err
			}
			list.set(count, 0)
			return read(ctx, 0, max)
		}
		return chatsTop{merging{w}, list}, nil, nil
	}
	size := int(req.GetLimit())
	if size <= 0 {
		size = server.WindowCap(chatBytes)
	}
	r := &chatRange{merging: merging{w}, chatList: list, start: around(index, size), size: size}
	w.items = func(ctx context.Context, _ int) ([]*v2.Upsert, error) {
		count, err := rs.r.ChatCount(ctx, f)
		if err != nil {
			return nil, err
		}
		start, size := r.span(count)
		out, err := read(ctx, start, size)
		if err != nil {
			return nil, err
		}
		r.read(start, len(out), count)
		list.set(count, start)
		return out, nil
	}
	return r, nil, nil
}

func (rs *Reads) chatView(ctx context.Context, s *server.Session, req *v2.Subscribe) (server.Window, *v2.SubscribeResult, error) {
	id := req.GetChat().GetChatId()
	if _, err := rs.chatByID(ctx, id); err != nil {
		return nil, nil, err
	}
	w := &win{params: req}
	w.items = func(ctx context.Context, max int) ([]*v2.Upsert, error) {
		gen := rs.previews.begin()
		c, err := rs.begin(ctx)
		if err != nil {
			return nil, err
		}
		ch, ok, err := c.chat(id)
		if err != nil || !ok {
			return nil, err
		}
		it := c.chatItem(gen, ch)
		it.SetId("")
		return one(it), c.finish()
	}
	w.wake = func(c core.Change) bool { return touches(c, chatKinds...) }
	return w, nil, nil
}

// replaced is the id a person or chat id folded into, "" when it still
// stands on its own.
func (rs *Reads) replaced(id string) string {
	w, err := rs.World(context.Background())
	if err != nil {
		return ""
	}
	if cur := rs.ids.Current(w, id); cur != id {
		return cur
	}
	return ""
}

// chatByID is the chat an id names, NOT_FOUND when none.
func (rs *Reads) chatByID(ctx context.Context, id string) (model.Chat, error) {
	c, err := rs.begin(ctx)
	if err != nil {
		return model.Chat{}, err
	}
	ch, ok, err := c.chat(id)
	if err != nil {
		return model.Chat{}, err
	}
	if !ok {
		return model.Chat{}, notFound("no chat %q", id)
	}
	return ch, nil
}

// chat is the chat id names as this read's world has it.
func (c *rc) chat(id string) (model.Chat, bool, error) {
	if id == "" {
		return model.Chat{}, false, nil
	}
	key, ok := c.ids.Key(c.w, id)
	if !ok {
		return model.Chat{}, false, nil
	}
	return c.r.ChatIn(c.ctx, c.w, key)
}

// chatItem is ch as a row, sorted pinned first, then by pin, then by recency.
func (c *rc) chatItem(gen uint64, ch model.Chat) *v2.Upsert {
	it := &v2.Upsert{}
	row := c.chatRow(gen, ch)
	c.wait(ch.Key, func(id, _ string) { it.SetId(id) })
	it.SetChat(row)
	sort := []byte{1}
	if ch.Pinned {
		sort[0] = 0
		sort = desc(sort, ch.PinT)
	} else {
		sort = desc(sort, 0)
	}
	sort = desc(sort, ch.LastT)
	it.SetSort(append(sort, ch.Key...))
	return it
}

func (c *rc) chatRow(gen uint64, ch model.Chat) *v2.ChatRow {
	row := v2.ChatRow_builder{
		Name:             ch.Name,
		Type:             chatType(ch.Key),
		LastMs:           ch.LastT,
		Unread:           uint32(max(ch.Unread, 0)),
		MarkedUnread:     ch.MarkedUnread,
		Pinned:           ch.Pinned,
		Archived:         ch.Archived,
		Muted:            ch.Muted,
		MuteEndMs:        toMS(ch.MuteEnd),
		HistoryExhausted: ch.Exhausted,
		EphemeralSecs:    uint32(max(ch.Ephemeral, 0)),
		ReadOnly:         ch.ReadOnly,
		LoadingOlder:     c.live.LoadingOlder(ch.Key),
	}.Build()
	c.wait(ch.Key, func(id, av string) { row.SetId(id); row.SetAvatarPath(av) })
	p, ok := c.previews.get(ch.Key)
	if !ok {
		var named map[string]bool
		p, named = c.preview(ch)
		c.previews.put(gen, ch.Key, ch.Addrs, named, p)
	}
	row.SetPreview(p)
	return row
}

func chatType(key string) v2.ChatType {
	_, server, _ := strings.Cut(key, "@")
	switch server {
	case types.GroupServer:
		return v2.ChatType_CHAT_TYPE_GROUP
	case types.NewsletterServer:
		return v2.ChatType_CHAT_TYPE_NEWSLETTER
	case types.BroadcastServer:
		return v2.ChatType_CHAT_TYPE_BROADCAST
	}
	return v2.ChatType_CHAT_TYPE_DIRECT
}

// preview is the chat's newest row as the list says it, nil for none, and
// everyone its text names. the row is built on a read of its own: nobody in
// it needs an id.
func (c *rc) preview(ch model.Chat) (*v2.ChatPreview, map[string]bool) {
	last, ok, err := c.r.Preview(c.ctx, c.w, ch.Addrs)
	if err != nil {
		c.log.Warn().Err(err).Str("chat", ch.Key).Msg("views: preview")
	}
	if !ok {
		return nil, nil
	}
	// its own decoder, so the names it uses land in its own shown
	pc := &rc{Reads: c.Reads, ctx: c.ctx, w: c.w}
	row, line := pc.build(chatOf(ch), last)
	if ch.Group && last.System == nil && line != "" {
		author := strings.TrimSpace(strings.TrimPrefix(row.GetSender().GetName(), "~"))
		if last.FromMe {
			author = "You"
		}
		if author != "" {
			line = author + ": " + line
		}
	}
	return v2.ChatPreview_builder{Text: line, FromMe: last.FromMe, Status: row.GetStatus()}.Build(), pc.shown
}
