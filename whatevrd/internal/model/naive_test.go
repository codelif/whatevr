package model

// the chat list as reads computed it before chat_row: every chat put together
// from its tables on each read. kept to check chat_row against.

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

// ChatsIn is Chats as w has it, for a caller that keeps its world.
func (r *Reader) naiveChatsIn(ctx context.Context, w *World, f ChatFilter) ([]Chat, error) {
	all, err := r.naiveStates(ctx, "")
	if err != nil {
		return nil, err
	}
	if err := r.naiveLoud(ctx, w, all, nil); err != nil {
		return nil, err
	}
	chats := r.naiveAssemble(w, all)
	out := chats[:0]
	name := strings.ToLower(strings.TrimSpace(f.Name))
	for _, c := range chats {
		if c.Archived != f.Archived && !f.Any {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(c.Name), name) {
			continue
		}
		switch f.Kind {
		case "direct":
			if c.Group {
				continue
			}
		case "groups":
			if !c.Group {
				continue
			}
		}
		out = append(out, c)
	}
	sortChats(out)
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	for i := range out {
		if err := r.naiveUnread(ctx, w, &out[i], all); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// chatStates reads the state of every address that is a chat, or of one
// key's addresses.
func (r *Reader) naiveStates(ctx context.Context, only string) (map[string]*chatState, error) {
	states := map[string]*chatState{}
	get := func(a string) *chatState {
		s := states[a]
		if s == nil {
			s = &chatState{as: map[string]asRow{}}
			states[a] = s
		}
		return s
	}
	var filter string
	var args []any
	if only != "" {
		addrs := []string{only}
		if isLID(only) {
			rows, err := r.db.QueryContext(ctx, `SELECT pn FROM id_map WHERE lid = ?`, only)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var pn string
				if rows.Scan(&pn) == nil {
					addrs = append(addrs, pn)
				}
			}
			rows.Close()
		}
		filter = ` WHERE chat IN (` + placeholders(len(addrs)) + `)`
		args = anys(addrs)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT chat, MAX(t) FROM msg`+filter+` GROUP BY chat`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var t int64
		if err := rows.Scan(&c, &t); err != nil {
			rows.Close()
			return nil, err
		}
		get(c).last = t
	}
	rows.Close()
	rows, err = r.db.QueryContext(ctx, `SELECT chat, MAX(t) FROM msg_wait`+filter+` GROUP BY chat`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var t int64
		if err := rows.Scan(&c, &t); err != nil {
			rows.Close()
			return nil, err
		}
		if s := get(c); t > s.last {
			s.last = t
		}
	}
	rows.Close()
	// a send still in the outbox makes its chat, as it makes a transcript row
	outbox := `SELECT chat, MAX(queued_t) FROM outbox o WHERE queued_t > 0 AND cancelled = 0
		AND NOT EXISTS (SELECT 1 FROM msg_src s WHERE s.id = o.id)`
	if filter != "" {
		outbox += ` AND` + strings.TrimPrefix(filter, " WHERE")
	}
	rows, err = r.db.QueryContext(ctx, outbox+` GROUP BY chat`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var t int64
		if err := rows.Scan(&c, &t); err != nil {
			rows.Close()
			return nil, err
		}
		if s := get(c); t > s.last {
			s.last = t
		}
	}
	rows.Close()
	rows, err = r.db.QueryContext(ctx, `SELECT chat, t, name, archived, pinned, mute_end, unread, marked_unread, ephemeral, read_only, end_type
		FROM hist_chat`+filter, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		h := &histChat{}
		if err := rows.Scan(&c, &h.t, &h.name, &h.archived, &h.pinned, &h.muteEnd, &h.unread, &h.markedUnread, &h.ephemeral, &h.ro, &h.endType); err != nil {
			rows.Close()
			return nil, err
		}
		get(c).hist = h
	}
	rows.Close()
	afilter := strings.ReplaceAll(filter, "chat IN", "a IN")
	if afilter == "" {
		afilter = " WHERE 1"
	}
	rows, err = r.db.QueryContext(ctx, `SELECT a, kind, op, on_, n, t, s FROM appstate`+afilter+` AND kind IN (?, ?, ?, ?, ?, ?)`,
		append(args, asPin, asArchive, asMute, asMarkRead, asDeleteChat, asClearChat)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, kind, op string
		var x asRow
		if err := rows.Scan(&a, &kind, &op, &x.on, &x.n, &x.t, &x.s); err != nil {
			rows.Close()
			return nil, err
		}
		if op != "set" {
			x = asRow{}
		}
		s := states[a]
		if s == nil && (kind == asDeleteChat || kind == asClearChat || !x.on) {
			continue
		}
		get(a).as[kind] = x
	}
	rows.Close()
	gfilter := strings.ReplaceAll(filter, "chat IN", "grp IN")
	nameQ := `SELECT grp, value FROM grp_field WHERE field = 'name'`
	if gfilter != "" {
		nameQ += ` AND` + strings.TrimPrefix(gfilter, " WHERE")
	}
	rows, err = r.db.QueryContext(ctx, nameQ, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g, n string
		if rows.Scan(&g, &n) == nil {
			if s := states[g]; s != nil {
				s.grpName = n
			}
		}
	}
	rows.Close()
	rows, err = r.db.QueryContext(ctx, `SELECT grp, error FROM grp_error`+gfilter, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g, e string
		if rows.Scan(&g, &e) == nil {
			if s := states[g]; s != nil {
				s.grpErr = e
			}
		}
	}
	rows.Close()
	return states, rows.Err()
}

// assemble folds address states into chat rows under their keys.
func (r *Reader) naiveAssemble(w *World, states map[string]*chatState) []Chat {
	byKey := map[string]*Chat{}
	var order []string
	for addr, s := range states {
		if addr == "status@broadcast" || addr == "" {
			continue
		}
		key := w.Key(addr, s.last)
		c := byKey[key]
		if c == nil {
			c = &Chat{Key: key, Group: server(key) == types.GroupServer}
			byKey[key] = c
			order = append(order, key)
		}
		c.Addrs = append(c.Addrs, addr)
		if s.last > c.LastT {
			c.LastT = s.last
		}
	}
	sort.Strings(order)
	out := make([]Chat, 0, len(order))
	now := r.now().UnixMilli()
	for _, key := range order {
		c := byKey[key]
		sort.Strings(c.Addrs)
		c.PN, c.LID = w.PN(key), w.LID(key)
		var hist *histChat
		as := map[string]asRow{}
		asT := map[string]int64{}
		for _, a := range c.Addrs {
			s := states[a]
			if s.hist != nil && (hist == nil || s.hist.t > hist.t) {
				hist = s.hist
			}
			if s.grpErr != "" {
				c.GroupError = s.grpErr
			}
			if s.grpName != "" {
				c.Name = s.grpName
			}
			for kind, x := range s.as {
				if old, ok := as[kind]; !ok || x.t > asT[kind] || x.t == asT[kind] && x.n > old.n {
					as[kind], asT[kind] = x, x.t
				}
			}
		}
		// a delete with nothing newer after it takes the chat off the list
		if d, ok := as[asDeleteChat]; ok && d.on && c.LastT <= d.n*1000 {
			continue
		}
		if x, ok := as[asPin]; ok {
			c.Pinned, c.PinT = x.on, x.t
		} else if hist != nil && hist.pinned > 0 {
			c.Pinned, c.PinT = true, hist.pinned*1000
		}
		if x, ok := as[asArchive]; ok {
			c.Archived = x.on
		} else if hist != nil {
			c.Archived = hist.archived
		}
		if x, ok := as[asMute]; ok {
			c.Muted, c.MuteEnd = x.on, x.n
		} else if hist != nil && hist.muteEnd != 0 {
			c.Muted, c.MuteEnd = true, hist.muteEnd
		}
		if c.Muted && c.MuteEnd > 0 && muteEndMS(c.MuteEnd) < now {
			c.Muted, c.MuteEnd = false, 0
		}
		if x, ok := as[asMarkRead]; ok {
			c.MarkedUnread = !x.on
		} else if hist != nil {
			c.MarkedUnread = hist.markedUnread
		}
		if hist != nil {
			c.Exhausted = hist.endType == 1 || hist.endType == 3
			c.Ephemeral = hist.ephemeral
			c.ReadOnly = hist.ro
		}
		c.Name, c.NameFrom = r.naiveName(w, c, hist)
		out = append(out, *c)
	}
	return out
}

func (r *Reader) naiveName(w *World, c *Chat, hist *histChat) (string, string) {
	switch {
	case c.Group:
		if c.Name != "" {
			return c.Name, "group"
		}
		if hist != nil && strings.TrimSpace(hist.name) != "" {
			return strings.TrimSpace(hist.name), "group"
		}
		return UnknownGroup, "group"
	case server(c.Key) == types.NewsletterServer || server(c.Key) == types.BroadcastServer:
		if hist != nil && hist.name != "" {
			return hist.name, "contact"
		}
		return "", "raw"
	}
	if w.IsSelf(c.Key) {
		if n := w.SelfName(); n != "" {
			return n + " (You)", "contact"
		}
		return "You", "contact"
	}
	if n, src := w.Name(c.Key); n != "" && src == NameContact {
		return n, src
	}
	if hist != nil && strings.TrimSpace(hist.name) != "" {
		return strings.TrimSpace(hist.name), NameContact
	}
	// the list shows an unsaved number as the number, as whatsapp does
	if pn := w.PN(c.Key); pn != "" {
		return FormatPhone(pn), "phone"
	}
	return w.Name(c.Key)
}

// unread counts what came in after the newest sign the chat was read: a
// read on the phone, a read here, or something we sent. history says how
// many were unread when it was cut; that holds until a newer sign.
func (r *Reader) naiveUnread(ctx context.Context, w *World, c *Chat, states map[string]*chatState) error {
	addrs := c.Addrs
	ph := placeholders(len(addrs))
	var horizon Cursor
	for _, a := range addrs {
		s := states[a]
		if s == nil {
			continue
		}
		if x, ok := s.as[asMarkRead]; ok && x.on {
			if m := (Cursor{T: x.n * 1000, Ord: math.MaxInt64}); m.after(horizon) {
				horizon = m
			}
		}
	}
	newest := func(q string, args ...any) error {
		var c Cursor
		err := r.db.QueryRowContext(ctx, q+` ORDER BY t DESC, ord DESC LIMIT 1`, args...).Scan(&c.T, &c.Ord)
		if isNoRows(err) {
			return nil
		}
		if err == nil && c.after(horizon) {
			horizon = c
		}
		return err
	}
	if err := newest(`SELECT t, ord FROM msg WHERE chat IN (`+ph+`) AND from_me = 1`, anys(addrs)...); err != nil {
		return err
	}
	// a read from this account's phone comes as read-self, or as a plain
	// read with from_me set
	if err := newest(`SELECT t, ord FROM (
		SELECT m.t, m.ord FROM f_receipt f JOIN msg m ON m.id = f.id AND m.chat IN (`+ph+`)
			WHERE f.type = 'read-self' AND f.chat IN (`+ph+`)
		UNION ALL
		SELECT m.t, m.ord FROM f_receipt f JOIN msg m ON m.id = f.id AND m.chat IN (`+ph+`)
			WHERE f.who = ? AND f.type IN ('read', 'played', 'played-self') AND f.chat IN (`+ph+`))`,
		append(append(append(append(anys(addrs), anys(addrs)...), anys(addrs)...), Me), anys(addrs)...)...); err != nil {
		return err
	}
	var hist *histChat
	for _, a := range addrs {
		if s := states[a]; s != nil && s.hist != nil && (hist == nil || s.hist.t > hist.t) {
			hist = s.hist
		}
	}
	from, base := horizon, 0
	// a read in history's own millisecond is of the message it was cut at,
	// so it ends history's count
	if hist != nil && hist.t > horizon.T {
		from, base = Cursor{T: hist.t, Ord: math.MaxInt64}, hist.unread
	}
	var n int
	// a message deleted for everyone before it was read is not waiting on anyone
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM msg m WHERE m.chat IN (`+ph+`) AND m.from_me = 0 AND (m.t, m.ord) > (?, ?)
		AND m.kind NOT LIKE 'stub:%' AND NOT EXISTS (SELECT 1 FROM f_revoke r WHERE r.target = m.id AND r.ok = 1 AND r.chat IN (`+ph+`))`,
		append(append(anys(addrs), from.T, from.Ord), anys(addrs)...)...).Scan(&n); err != nil {
		return err
	}
	sys, err := r.systemRows(ctx, addrs, `(m.t, m.ord) > (?, ?) AND m.who != ''`, "ASC", []any{from.T, from.Ord}, 1000)
	if err != nil {
		return err
	}
	for _, m := range sys {
		if w.Loud(m.System) {
			n++
		}
	}
	c.Unread = base + n
	return nil
}

// loudSystem adds the loud system rows of the chats in states to their last
// time, so one that names this account brings its chat up the list.
func (r *Reader) naiveLoud(ctx context.Context, w *World, states map[string]*chatState, only []string) error {
	q := `SELECT ` + sysCols + ` FROM sys_msg s WHERE s.who != ''`
	var args []any
	if len(only) > 0 {
		q += ` AND s.chat IN (` + placeholders(len(only)) + `)`
		args = anys(only)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanSystem(rows)
		if err != nil {
			return err
		}
		if !w.Loud(m.System) {
			continue
		}
		s := states[m.Chat]
		if s == nil {
			s = &chatState{as: map[string]asRow{}}
			states[m.Chat] = s
		}
		if m.T > s.last {
			s.last = m.T
		}
	}
	return rows.Err()
}

func sortChats(cs []Chat) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.Pinned && a.PinT != b.PinT {
			return a.PinT > b.PinT
		}
		if a.LastT != b.LastT {
			return a.LastT > b.LastT
		}
		return a.Key < b.Key
	})
}

// checkSummary fails when chat_row says anything the old read-time code
// would not have.
func checkSummary(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	r := NewReader(db)
	w, err := r.World(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ChatsIn(ctx, w, ChatFilter{Any: true})
	if err != nil {
		t.Fatal(err)
	}
	want, err := r.naiveChatsIn(ctx, w, ChatFilter{Any: true})
	if err != nil {
		t.Fatal(err)
	}
	strip := func(cs []Chat) []string {
		var out []string
		for _, c := range cs {
			c.grpName, c.histName = "", ""
			// an unpin's time orders nothing
			if !c.Pinned {
				c.PinT = 0
			}
			out = append(out, fmt.Sprintf("%+v", c))
		}
		return out
	}
	g, wa := strip(got), strip(want)
	if !reflect.DeepEqual(g, wa) {
		for i := 0; i < max(len(g), len(wa)); i++ {
			var x, y string
			if i < len(g) {
				x = g[i]
			}
			if i < len(wa) {
				y = wa[i]
			}
			if x != y {
				t.Errorf("chat %d:\n  chat_row %s\n  read     %s", i, x, y)
			}
		}
		t.FailNow()
	}
}
