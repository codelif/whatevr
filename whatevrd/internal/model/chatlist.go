package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"go.mau.fi/whatsmeow/types"

	"whatevrd/internal/core"
)

// chatlist keeps the chat list as rows, one per chat, summed up from every
// table a row is made of in the same commit that changed them. a list read is
// then one small table instead of a pass over every message. the parts that
// hang on who is who now (names, which number) and the clock (a mute running
// out) are still put on at read time.
var chatlistDomain = core.Domain{
	Name:    "chatlist",
	Version: 1,
	Tables:  []string{"chat_row", "chat_addr"},
	Schema: []string{
		`CREATE TABLE chat_row (
			key           TEXT PRIMARY KEY,
			addrs         TEXT NOT NULL,
			grp           INTEGER NOT NULL,
			last_t        INTEGER NOT NULL,
			pinned        INTEGER NOT NULL,
			pin_t         INTEGER NOT NULL,
			archived      INTEGER NOT NULL,
			muted         INTEGER NOT NULL,
			mute_end      INTEGER NOT NULL,
			unread        INTEGER NOT NULL,
			marked_unread INTEGER NOT NULL,
			exhausted     INTEGER NOT NULL,
			ephemeral     INTEGER NOT NULL,
			read_only     INTEGER NOT NULL,
			group_error   TEXT NOT NULL,
			grp_name      TEXT NOT NULL,
			hist_name     TEXT NOT NULL,
			deleted       INTEGER NOT NULL
		) WITHOUT ROWID`,
		// the list's order, so a window reads only its own rows
		`CREATE INDEX chat_row_order ON chat_row (deleted, archived, pinned DESC, pin_t DESC, last_t DESC, key)`,
		// which row each address is in
		`CREATE TABLE chat_addr (
			addr TEXT PRIMARY KEY,
			key  TEXT NOT NULL
		) WITHOUT ROWID`,
		`CREATE INDEX chat_addr_key ON chat_addr (key)`,
	},
	Watch:  []string{"chat", "person"},
	Finish: finishChats,
}

// finishChats sums up again every chat a batch touched. a touched address
// may have moved between chats (a lid learned, a number given to someone
// new), so the row it was in and every row it could be in now are all done.
func finishChats(tx *core.Tx, touched map[string][]string, all map[string]bool) error {
	ctx := tx.Context()
	r := &Reader{db: tx}
	addrs := touched["chat"]
	if all["chat"] {
		var err error
		if addrs, err = r.chatAddrs(ctx); err != nil {
			return err
		}
	}
	if selfMoved(ctx, tx, touched["person"], all["person"]) {
		// whether a system row names this account changed
		more, err := strs(ctx, tx, `SELECT DISTINCT chat FROM sys_msg WHERE who != ''`)
		if err != nil {
			return err
		}
		addrs = append(addrs, more...)
	}
	if len(addrs) == 0 {
		return nil
	}
	keys := map[string]bool{}
	for _, a := range addrs {
		if a != "" {
			keys[a] = true
		}
	}
	list := setList(keys)
	was, err := strs(ctx, tx, `SELECT key FROM chat_addr WHERE addr IN (SELECT value FROM json_each(?))`, jsonList(list))
	if err != nil {
		return err
	}
	owners, err := strs(ctx, tx, `SELECT lid FROM id_map WHERE pn IN (SELECT value FROM json_each(?))`, jsonList(list))
	if err != nil {
		return err
	}
	for _, k := range append(was, owners...) {
		keys[k] = true
	}
	w, err := keyWorld(ctx, tx, setList(keys))
	if err != nil {
		return err
	}
	span := map[string]bool{}
	for k := range keys {
		for _, a := range w.Addrs(k) {
			span[a] = true
		}
	}
	chats, err := r.summarize(ctx, w, setList(span))
	if err != nil {
		return err
	}
	for k := range keys {
		if _, err := tx.Exec(`DELETE FROM chat_addr WHERE key = ?`, k); err != nil {
			return err
		}
	}
	made := map[string]bool{}
	for _, c := range chats {
		if !keys[c.Key] {
			// an address of ours that belongs to a chat not touched here
			continue
		}
		made[c.Key] = true
		if err := putRow(tx, c); err != nil {
			return err
		}
	}
	for k := range keys {
		if made[k] {
			continue
		}
		res, err := tx.Exec(`DELETE FROM chat_row WHERE key = ?`, k)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			tx.Touch("chatrow", k)
		}
	}
	return nil
}

// selfMoved says a batch changed which addresses are this account.
func selfMoved(ctx context.Context, tx *core.Tx, persons []string, all bool) bool {
	if all {
		return true
	}
	if len(persons) == 0 {
		return false
	}
	for _, p := range persons {
		if p == "self" {
			return true
		}
	}
	var one int
	return tx.QueryRowContext(ctx, `SELECT 1 FROM id_self WHERE jid IN (SELECT value FROM json_each(?)) LIMIT 1`,
		jsonList(persons)).Scan(&one) == nil
}

func putRow(tx *core.Tx, c Chat) error {
	res, err := tx.Exec(`INSERT INTO chat_row (`+rowCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET addrs = excluded.addrs, grp = excluded.grp, last_t = excluded.last_t,
			pinned = excluded.pinned, pin_t = excluded.pin_t, archived = excluded.archived, muted = excluded.muted,
			mute_end = excluded.mute_end, unread = excluded.unread, marked_unread = excluded.marked_unread,
			exhausted = excluded.exhausted, ephemeral = excluded.ephemeral, read_only = excluded.read_only,
			group_error = excluded.group_error, grp_name = excluded.grp_name, hist_name = excluded.hist_name,
			deleted = excluded.deleted
		WHERE (addrs, grp, last_t, pinned, pin_t, archived, muted, mute_end, unread, marked_unread, exhausted,
			ephemeral, read_only, group_error, grp_name, hist_name, deleted) IS NOT
			(excluded.addrs, excluded.grp, excluded.last_t, excluded.pinned, excluded.pin_t, excluded.archived,
			excluded.muted, excluded.mute_end, excluded.unread, excluded.marked_unread, excluded.exhausted,
			excluded.ephemeral, excluded.read_only, excluded.group_error, excluded.grp_name, excluded.hist_name,
			excluded.deleted)`,
		c.Key, strings.Join(c.Addrs, ","), c.Group, c.LastT, c.Pinned, c.PinT, c.Archived, c.Muted, c.MuteEnd,
		c.Unread, c.MarkedUnread, c.Exhausted, c.Ephemeral, c.ReadOnly, c.GroupError, c.grpName, c.histName, c.deleted)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		tx.Touch("chatrow", c.Key)
	}
	for _, a := range c.Addrs {
		if _, err := tx.Exec(`INSERT INTO chat_addr (addr, key) VALUES (?, ?)
			ON CONFLICT (addr) DO UPDATE SET key = excluded.key`, a, c.Key); err != nil {
			return err
		}
	}
	return nil
}

// keyWorld is the part of World that says which chat an address is in, for
// keys and every address that can join them.
func keyWorld(ctx context.Context, q querier, keys []string) (*World, error) {
	w := &World{self: map[string]bool{}}
	self, err := strs(ctx, q, `SELECT jid FROM id_self`)
	if err != nil {
		return nil, err
	}
	for _, j := range self {
		w.self[j] = true
	}
	// a number of this account's lid answers IsSelf through its owner
	lids := append(append([]string{}, keys...), self...)
	rows, err := q.QueryContext(ctx, `SELECT lid, pn FROM id_map WHERE lid IN (SELECT value FROM json_each(?))
		ORDER BY lid, since DESC, pn`, jsonList(lids))
	if err != nil {
		return nil, err
	}
	pns := map[string]bool{}
	for rows.Next() {
		var lid, pn string
		if err := rows.Scan(&lid, &pn); err != nil {
			rows.Close()
			return nil, err
		}
		if got := w.pns.get(lid); len(got) == 0 || got[len(got)-1] != pn {
			w.pns.set(lid, append(got, pn))
		}
		pns[pn] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, k := range keys {
		if isPN(k) {
			pns[k] = true
		}
	}
	rows, err = q.QueryContext(ctx, `SELECT pn, from_t, to_t, lid FROM id_owner WHERE pn IN (SELECT value FROM json_each(?))
		ORDER BY pn, from_t`, jsonList(setList(pns)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pn string
		var s span
		if err := rows.Scan(&pn, &s.from, &s.to, &s.lid); err != nil {
			return nil, err
		}
		w.owners.set(pn, append(w.owners.get(pn), s))
	}
	return w, rows.Err()
}

// chatAddrs is every address something is filed under that can make a chat.
func (r *Reader) chatAddrs(ctx context.Context) ([]string, error) {
	return strs(ctx, r.db, `SELECT DISTINCT chat FROM msg UNION SELECT chat FROM msg_wait UNION SELECT chat FROM outbox
		UNION SELECT chat FROM hist_chat UNION SELECT a FROM appstate WHERE kind IN (?, ?, ?, ?, ?, ?)
		UNION SELECT chat FROM sys_msg UNION SELECT key FROM chat_row`,
		asPin, asArchive, asMute, asMarkRead, asDeleteChat, asClearChat)
}

// summarize puts together the chat rows of addrs: every chat one of them is
// in, complete as long as addrs holds all of that chat's addresses.
func (r *Reader) summarize(ctx context.Context, w *World, addrs []string) ([]Chat, error) {
	states, err := r.chatStates(ctx, addrs)
	if err != nil {
		return nil, err
	}
	if err := r.loudSystem(ctx, w, states, addrs); err != nil {
		return nil, err
	}
	chats := assemble(w, states)
	for i := range chats {
		if chats[i].deleted {
			continue
		}
		if err := r.unread(ctx, w, &chats[i], states); err != nil {
			return nil, err
		}
	}
	return chats, nil
}

// chatStates reads the state of each address. every query is a seek per
// address: a GROUP BY over an IN list walks each chat's whole index.
func (r *Reader) chatStates(ctx context.Context, addrs []string) (map[string]*chatState, error) {
	states := map[string]*chatState{}
	get := func(a string) *chatState {
		s := states[a]
		if s == nil {
			s = &chatState{as: map[string]asRow{}}
			states[a] = s
		}
		return s
	}
	in := jsonList(addrs)
	newest := func(q string) error {
		rows, err := r.db.QueryContext(ctx, q, in)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c string
			var t sql.NullInt64
			if err := rows.Scan(&c, &t); err != nil {
				return err
			}
			if !t.Valid {
				continue
			}
			if s := get(c); t.Int64 > s.last {
				s.last = t.Int64
			}
		}
		return rows.Err()
	}
	for _, q := range []string{
		`SELECT j.value, (SELECT MAX(t) FROM msg WHERE chat = j.value) FROM json_each(?) j`,
		`SELECT chat, MAX(t) FROM msg_wait WHERE chat IN (SELECT value FROM json_each(?)) GROUP BY chat`,
		// a send still in the outbox makes its chat, as it makes a transcript row
		`SELECT chat, MAX(queued_t) FROM outbox o WHERE chat IN (SELECT value FROM json_each(?)) AND queued_t > 0
			AND cancelled = 0 AND NOT EXISTS (SELECT 1 FROM msg_src s WHERE s.id = o.id) GROUP BY chat`,
	} {
		if err := newest(q); err != nil {
			return nil, err
		}
	}
	rows, err := r.db.QueryContext(ctx, `SELECT chat, t, name, archived, pinned, mute_end, unread, marked_unread, ephemeral, read_only, end_type
		FROM hist_chat WHERE chat IN (SELECT value FROM json_each(?))`, in)
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
	rows, err = r.db.QueryContext(ctx, `SELECT a, kind, op, on_, n, t, s FROM appstate
		WHERE kind IN (?, ?, ?, ?, ?, ?) AND a IN (SELECT value FROM json_each(?))`,
		asPin, asArchive, asMute, asMarkRead, asDeleteChat, asClearChat, in)
	if err != nil {
		return nil, err
	}
	type asIn struct {
		a, kind string
		x       asRow
	}
	var ass []asIn
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
		ass = append(ass, asIn{a, kind, x})
	}
	rows.Close()
	// app state alone makes a chat only by switching something on: a delete,
	// a clear or an unpin of a chat nothing else knows makes none
	for _, x := range ass {
		if s := states[x.a]; s == nil && (x.kind == asDeleteChat || x.kind == asClearChat || !x.x.on) {
			continue
		}
		get(x.a).as[x.kind] = x.x
	}
	rows, err = r.db.QueryContext(ctx, `SELECT grp, value FROM grp_field WHERE field = 'name' AND grp IN (SELECT value FROM json_each(?))`, in)
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
	rows, err = r.db.QueryContext(ctx, `SELECT grp, error FROM grp_error WHERE grp IN (SELECT value FROM json_each(?))`, in)
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
func assemble(w *World, states map[string]*chatState) []Chat {
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
	for _, key := range order {
		c := byKey[key]
		sort.Strings(c.Addrs)
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
				c.grpName = s.grpName
			}
			for kind, x := range s.as {
				if old, ok := as[kind]; !ok || x.t > asT[kind] || x.t == asT[kind] && x.n > old.n {
					as[kind], asT[kind] = x, x.t
				}
			}
		}
		// a delete with nothing newer after it takes the chat off the list
		if d, ok := as[asDeleteChat]; ok && d.on && c.LastT <= d.n*1000 {
			c.deleted = true
		}
		if x, ok := as[asPin]; ok {
			c.Pinned, c.PinT = x.on, x.t
		} else if hist != nil && hist.pinned > 0 {
			c.Pinned, c.PinT = true, hist.pinned*1000
		}
		if !c.Pinned {
			c.PinT = 0
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
		if x, ok := as[asMarkRead]; ok {
			c.MarkedUnread = !x.on
		} else if hist != nil {
			c.MarkedUnread = hist.markedUnread
		}
		if hist != nil {
			c.Exhausted = hist.endType == 1 || hist.endType == 3
			c.Ephemeral = hist.ephemeral
			c.ReadOnly = hist.ro
			c.histName = hist.name
		}
		out = append(out, *c)
	}
	return out
}

// unread counts what came in after the newest sign the chat was read: a
// read on the phone, a read here, or something we sent. history says how
// many were unread when it was cut; that holds until a newer sign.
func (r *Reader) unread(ctx context.Context, w *World, c *Chat, states map[string]*chatState) error {
	addrs := c.Addrs
	in := jsonList(addrs)
	// a mark on the phone covers its whole second
	var horizon Cursor
	var hist *histChat
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
		if s.hist != nil && (hist == nil || s.hist.t > hist.t) {
			hist = s.hist
		}
	}
	seen, err := r.readTo(ctx, in)
	if err != nil {
		return err
	}
	if seen.after(horizon) {
		horizon = seen
	}
	from, base := horizon, 0
	// a read in history's own millisecond is of the message it was cut at,
	// so it ends history's count
	if hist != nil && hist.t > horizon.T {
		from, base = Cursor{T: hist.t, Ord: math.MaxInt64}, hist.unread
	}
	// a message deleted for everyone before it was read is not waiting on
	// anyone: counted on the index of incoming messages, the deleted taken
	// off after, starting from the few deletes (cross join keeps that order)
	var n, gone int
	if err := r.db.QueryRowContext(ctx, `SELECT SUM((SELECT COUNT(*) FROM msg WHERE chat = j.value AND from_me = 0
		AND kind NOT LIKE 'stub:%' AND (t, ord) > (?, ?))) FROM json_each(?) j`, from.T, from.Ord, in).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT DISTINCT m.chat, m.id FROM f_revoke r
			CROSS JOIN msg m ON m.id = r.target AND m.chat IN (SELECT value FROM json_each(?))
			WHERE r.chat IN (SELECT value FROM json_each(?)) AND r.ok = 1 AND m.from_me = 0 AND m.kind NOT LIKE 'stub:%' AND (m.t, m.ord) > (?, ?))`,
			in, in, from.T, from.Ord).Scan(&gone); err != nil {
			return err
		}
	}
	sys, err := r.systemRows(ctx, addrs, `(m.t, m.ord) > (?, ?) AND m.who != ''`, "ASC", []any{from.T, from.Ord}, 1000)
	if err != nil {
		return err
	}
	loud := 0
	for _, m := range sys {
		if w.Loud(m.System) {
			loud++
		}
	}
	c.Unread = base + n - gone + loud
	return nil
}

func strs(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func setList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func jsonList(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ss)
	return string(b)
}
