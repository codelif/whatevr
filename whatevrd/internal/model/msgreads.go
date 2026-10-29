package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"google.golang.org/protobuf/proto"
)

// Message is one transcript row with its body and everything said about it.
type Message struct {
	// Chat is the address it came under, which may be one of several a chat
	// row folds together
	Chat string
	// Home is the address the first copy to arrive came under: a second copy
	// under the chat's other address may have the better body, but the
	// message is still named by the first
	Home      string
	ID        string
	Sender    string
	SenderAlt string
	FromMe    bool
	T         int64
	// Ord is where it came among messages sharing T, see arrival
	Ord      int64
	Kind     string
	Text     string
	Reply    string
	Album    string
	ViewOnce bool
	// Status is history's status for our own messages, see histStatus
	Status int
	Src    int
	// History says Body is a HistorySyncMsg, otherwise a waE2E.Message
	History bool
	Body    []byte
	// Wait is set for a message that has not decrypted: "" is waiting on a
	// resend, anything else is whatsapp's reason it never will here
	Waiting bool
	Wait    string
	// Queued is a send of ours still in the outbox, Out says how it is going
	Queued bool
	Out    Outgoing
	// System is a row the chat wrote about itself, with no message behind it
	System *System

	Facts Facts

	// row is the msg rowid, which grows in arrival order; 0 for a stub
	row int64
}

// Facts is what other messages and app state said about one message.
type Facts struct {
	Reactions []Reaction
	// Edit is the newest content it was edited to
	Edit     *waE2E.Message
	EditT    int64
	Revoked  bool
	RevokeBy string
	RevokeT  int64
	Starred  bool
	Pinned   bool
	PinT     int64
	// PinEnd is when the pin runs out, a week after it when it did not say
	PinEnd   int64
	Kept     bool
	Votes    []Sealed
	Events   []Sealed
	Receipts []Receipt
	Local    Local
	// Live is the share a live location row opened, nil for anything else
	Live *Live
}

type Reaction struct {
	Sender string
	Emoji  string
	T      int64
}

// Sealed is a vote or event response, opened.
type Sealed struct {
	Sender string
	T      int64
	Plain  []byte
}

type Receipt struct {
	Who  string
	Type string
	T    int64
}

// Content is the message itself out of its body: the history envelope
// peeled, unwrapped. nil for a waiting row or a body that will not decode.
func (m Message) Content() (*waE2E.Message, *waHistorySync.HistorySyncMsg) {
	if len(m.Body) == 0 {
		return nil, nil
	}
	if m.History {
		var hm waHistorySync.HistorySyncMsg
		if proto.Unmarshal(m.Body, &hm) != nil {
			return nil, nil
		}
		return hm.GetMessage().GetMessage(), &hm
	}
	var raw waE2E.Message
	if proto.Unmarshal(m.Body, &raw) != nil {
		return nil, nil
	}
	return &raw, nil
}

// readTo is the newest sign the chat at the addresses in json list in was
// read: something we sent, or a read of ours. what comes after it in the
// transcript is unread.
func (r *Reader) readTo(ctx context.Context, in string) (Cursor, error) {
	var to Cursor
	// one seek per address on msg_out
	for _, q := range []string{`SELECT m.t, m.ord FROM json_each(?1) j CROSS JOIN msg m
		ON m.rowid = (SELECT rowid FROM msg WHERE chat = j.value AND from_me = 1 ORDER BY t DESC, ord DESC LIMIT 1)`,
		`SELECT t, ord FROM f_seen WHERE rchat IN (SELECT value FROM json_each(?1))
		AND mchat IN (SELECT value FROM json_each(?1))`} {
		rows, err := r.db.QueryContext(ctx, q, in)
		if err != nil {
			return to, err
		}
		for rows.Next() {
			var c Cursor
			if err := rows.Scan(&c.T, &c.Ord); err != nil {
				rows.Close()
				return to, err
			}
			if c.after(to) {
				to = c
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return to, err
		}
	}
	return to, nil
}

// after says c comes later than d, ids aside.
func (c Cursor) after(d Cursor) bool { return c.T > d.T || c.T == d.T && c.Ord > d.Ord }

// Cursor is a place in a chat's transcript.
type Cursor struct {
	T   int64
	Ord int64
	ID  string
}

const msgCols = `m.chat, m.id, m.sender, m.sender_alt, m.from_me, m.t, m.kind, m.text, m.reply, m.album, m.view_once,
	m.status, m.src, m.off IS NOT NULL, m.body, m.rowid, m.ord`

func scanMsg(rows *sql.Rows) (Message, error) {
	var m Message
	err := rows.Scan(&m.Chat, &m.ID, &m.Sender, &m.SenderAlt, &m.FromMe, &m.T, &m.Kind, &m.Text, &m.Reply, &m.Album,
		&m.ViewOnce, &m.Status, &m.Src, &m.History, &m.Body, &m.row, &m.Ord)
	m.Home = m.Chat
	return m, err
}

// albumHidden is the sql for "this row is a picture inside an album that is
// here": the album row carries it.
// page is the rows q picks, with their bodies: q selects rowids from msg m,
// sorts and limits, so only the page's bodies are read.
func page(q, order string) string {
	return `SELECT ` + msgCols + ` FROM (` + q + `) k JOIN msg m ON m.rowid = k.r ORDER BY m.t ` + order + `, m.ord ` + order + `, m.id ` + order
}

// byChat is the first rows by time of each of n addresses that match where,
// with their bodies: one ordered index walk per address, as an IN over two
// would sort the whole chat to take a page.
func byChat(n int, where, order string) string {
	part := `SELECT * FROM (SELECT m.rowid AS r FROM msg m WHERE m.chat = ? AND ` + where +
		` ORDER BY m.t ` + order + `, m.ord ` + order + `, m.id ` + order + ` LIMIT ?)`
	parts := make([]string, n)
	for i := range parts {
		parts[i] = part
	}
	return `SELECT ` + msgCols + ` FROM (` + strings.Join(parts, ` UNION ALL `) + `) k
		JOIN msg m ON m.rowid = k.r
		ORDER BY m.t ` + order + `, m.ord ` + order + `, m.id ` + order + ` LIMIT ?`
}

func byChatArgs(addrs []string, where []any, limit int) []any {
	var args []any
	for _, a := range addrs {
		args = append(append(append(args, a), where...), limit)
	}
	return append(args, limit)
}

const albumHidden = `NOT (m.album != '' AND EXISTS (SELECT 1 FROM msg p WHERE p.id = m.album AND p.chat = m.chat))`

// MessageCount is how many rows the transcript of addrs shows, those before
// at only when at is set. a message under two addresses counts twice, which
// folding would cost a lookup per row to catch.
func (r *Reader) MessageCount(ctx context.Context, addrs []string, at *Cursor) (int, error) {
	ph := placeholders(len(addrs))
	cmp := func(t, ord, id string) string {
		if at == nil {
			return `1`
		}
		return `(` + t + `, ` + ord + `, ` + id + `) < (?, ?, ?)`
	}
	part := func(where []any) []any {
		args := anys(addrs)
		if at != nil {
			args = append(args, at.T, at.Ord, at.ID)
		}
		return append(args, where...)
	}
	q := `SELECT
		(SELECT count(*) FROM msg m WHERE m.chat IN (` + ph + `) AND ` + cmp("m.t", "m.ord", "m.id") + `)
		- (SELECT count(*) FROM msg m WHERE m.chat IN (` + ph + `) AND m.album != '' AND ` + cmp("m.t", "m.ord", "m.id") + `
			AND EXISTS (SELECT 1 FROM msg p WHERE p.id = m.album AND p.chat = m.chat))
		+ (SELECT count(*) FROM msg_wait w WHERE w.chat IN (` + ph + `) AND ` + cmp("w.t", "w.ord", "w.id") + `
			AND NOT EXISTS (SELECT 1 FROM msg m WHERE m.id = w.id AND m.chat IN (` + ph + `)))
		+ (SELECT count(*) FROM outbox o WHERE o.chat IN (` + ph + `) AND o.queued_t > 0 AND o.cancelled = 0
			AND ` + cmp("o.queued_t", "o.ord", "o.id") + ` AND NOT EXISTS (SELECT 1 FROM msg_src s WHERE ` + sentCopy + `))
		+ (SELECT count(*) FROM sys_msg s WHERE s.chat IN (` + ph + `) AND ` + cmp("s.t", "s.ord", "s.id") + `)`
	var args []any
	args = append(args, part(nil)...)
	args = append(args, part(nil)...)
	args = append(args, part(anys(addrs))...)
	args = append(args, part(nil)...)
	args = append(args, part(nil)...)
	var n int
	err := r.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return max(n, 0), err
}

// MessageAt is the stored message index rows from the oldest of addrs' total,
// walked from whichever end is nearer and clamped to the transcript. album
// pictures and rows outside msg are not skipped, so it lands near index,
// not on it.
func (r *Reader) MessageAt(ctx context.Context, addrs []string, index, total int) (Cursor, bool, error) {
	if len(addrs) == 0 || total <= 0 {
		return Cursor{}, false, nil
	}
	index = min(max(index, 0), total-1)
	order, off := `ASC`, index
	if index > total/2 {
		order, off = `DESC`, total-1-index
	}
	var c Cursor
	err := r.db.QueryRowContext(ctx, `SELECT m.t, m.ord, m.id FROM msg m WHERE m.chat IN (`+placeholders(len(addrs))+`)
		ORDER BY m.t `+order+`, m.ord `+order+`, m.id `+order+` LIMIT 1 OFFSET ?`, append(anys(addrs), off)...).
		Scan(&c.T, &c.Ord, &c.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, false, nil
	}
	return c, err == nil, err
}

// Messages pages through the transcript of addrs from cursor (exclusive;
// zero for the newest end), newest first unless asc.
func (r *Reader) Messages(ctx context.Context, addrs []string, from Cursor, limit int, asc bool) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	ph := placeholders(len(addrs))
	cmp, order := `(m.t, m.ord, m.id) < (?, ?, ?)`, `DESC`
	if asc {
		cmp, order = `(m.t, m.ord, m.id) > (?, ?, ?)`, `ASC`
	}
	if from == (Cursor{}) {
		if asc {
			from = Cursor{T: tMin + 1}
		} else {
			from = Cursor{T: tMax, ID: ""}
		}
	}
	args := append(anys(addrs), from.T, from.Ord, from.ID)
	// twice the page over two addresses: a message under both comes twice
	// and folds
	n := limit
	if len(addrs) > 1 {
		n *= 2
	}
	rows, err := r.db.QueryContext(ctx, byChat(len(addrs), cmp+` AND `+albumHidden, order),
		byChatArgs(addrs, []any{from.T, from.Ord, from.ID}, n)...)
	if err != nil {
		return nil, err
	}
	var out []Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	waits, err := r.db.QueryContext(ctx, `SELECT w.chat, w.id, w.sender, w.from_me, w.t, w.ord, w.unavailable FROM msg_wait w
		WHERE w.chat IN (`+ph+`) AND `+strings.ReplaceAll(cmp, "m.", "w.")+`
		AND NOT EXISTS (SELECT 1 FROM msg m WHERE m.id = w.id AND m.chat IN (`+ph+`))
		ORDER BY w.t `+order+`, w.ord `+order+`, w.id `+order+` LIMIT ?`, append(append(args, anys(addrs)...), limit)...)
	if err != nil {
		return nil, err
	}
	for waits.Next() {
		m := Message{Waiting: true, Kind: "waiting"}
		if err := waits.Scan(&m.Chat, &m.ID, &m.Sender, &m.FromMe, &m.T, &m.Ord, &m.Wait); err != nil {
			waits.Close()
			return nil, err
		}
		out = append(out, m)
	}
	waits.Close()
	queued, err := r.queuedRows(ctx, addrs, cmp, order, []any{from.T, from.Ord, from.ID}, limit)
	if err != nil {
		return nil, err
	}
	out = append(out, queued...)
	sys, err := r.systemRows(ctx, addrs, cmp, order, []any{from.T, from.Ord, from.ID}, limit)
	if err != nil {
		return nil, err
	}
	out = append(out, sys...)
	out = dedupe(out)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].T != out[j].T {
			return out[i].T > out[j].T != asc
		}
		if out[i].Ord != out[j].Ord {
			return out[i].Ord > out[j].Ord != asc
		}
		return out[i].ID > out[j].ID != asc
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, r.facts(ctx, out)
}

// Preview is the newest row that speaks for the chat in the list: a quiet
// system row does not.
func (r *Reader) Preview(ctx context.Context, w *World, addrs []string) (Message, bool, error) {
	from := Cursor{}
	// one row at a time: the newest nearly always speaks
	for range 64 {
		ms, err := r.Messages(ctx, addrs, from, 1, false)
		if err != nil || len(ms) == 0 {
			return Message{}, false, err
		}
		for _, m := range ms {
			if m.System == nil || w.Loud(m.System) {
				return m, true, nil
			}
		}
		last := ms[len(ms)-1]
		from = Cursor{T: last.T, Ord: last.Ord, ID: last.ID}
	}
	return Message{}, false, nil
}

// stub is a row with no message behind it yet.
func stub(m Message) bool { return m.Waiting || m.Queued }

// dedupe keeps one row per message id: the better body, named by the
// first copy. it reuses ms.
func dedupe(ms []Message) []Message {
	best := make(map[string]int, len(ms))
	out := ms[:0]
	for _, m := range ms {
		i, ok := best[m.ID]
		if !ok {
			if m.Home == "" {
				m.Home = m.Chat
			}
			best[m.ID] = len(out)
			out = append(out, m)
			continue
		}
		home, row := out[i].Home, out[i].row
		if m.row > 0 && (row == 0 || m.row < row) {
			home, row = m.Home, m.row
		}
		if m.Src > out[i].Src || stub(out[i]) && !stub(m) {
			out[i] = m
		}
		out[i].Home, out[i].row = home, row
	}
	return out
}

// Message is one message by id under any of addrs.
func (r *Reader) Message(ctx context.Context, addrs []string, id string) (Message, bool, error) {
	ph := placeholders(len(addrs))
	rows, err := r.db.QueryContext(ctx, `SELECT `+msgCols+` FROM msg m
		WHERE m.id = ? AND m.chat IN (`+ph+`) ORDER BY m.src DESC`, append([]any{id}, anys(addrs)...)...)
	if err != nil {
		return Message{}, false, err
	}
	var out []Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			rows.Close()
			return Message{}, false, err
		}
		out = append(out, m)
	}
	rows.Close()
	if len(out) > 1 {
		for _, m := range out[1:] {
			if m.row < out[0].row {
				out[0].Home, out[0].row = m.Home, m.row
			}
		}
		out = out[:1]
	}
	if len(out) == 0 {
		m := Message{Waiting: true, Kind: "waiting"}
		err := r.db.QueryRowContext(ctx, `SELECT chat, id, sender, from_me, t, ord, unavailable FROM msg_wait WHERE id = ? AND chat IN (`+ph+`)`,
			append([]any{id}, anys(addrs)...)...).Scan(&m.Chat, &m.ID, &m.Sender, &m.FromMe, &m.T, &m.Ord, &m.Wait)
		m.Home = m.Chat
		if isNoRows(err) {
			o, err := scanOutgoing(r.db.QueryRowContext(ctx, `SELECT `+outgoingCols+` FROM outbox o
				WHERE o.id = ? AND o.chat IN (`+ph+`) AND o.queued_t > 0 AND o.cancelled = 0`, append([]any{id}, anys(addrs)...)...))
			if isNoRows(err) {
				m, err := scanSystem(r.db.QueryRowContext(ctx, `SELECT `+sysCols+` FROM sys_msg s WHERE s.id = ? AND s.chat IN (`+ph+`)`,
					append([]any{id}, anys(addrs)...)...))
				if isNoRows(err) {
					return Message{}, false, nil
				}
				return m, err == nil, err
			}
			if err != nil {
				return Message{}, false, err
			}
			return queuedMessage(o), true, nil
		}
		if err != nil {
			return Message{}, false, err
		}
		out = append(out, m)
	}
	return out[0], true, r.facts(ctx, out)
}

// AlbumChildren is the pictures an album row groups, in order.
func (r *Reader) AlbumChildren(ctx context.Context, chat, album string) ([]Message, error) {
	// album != '' lets the partial index in, +chat keeps the chat's out
	rows, err := r.db.QueryContext(ctx, `SELECT `+msgCols+` FROM msg m
		WHERE m.album = ? AND m.album != '' AND +m.chat = ? ORDER BY m.t, m.ord, m.id`, album, chat)
	if err != nil {
		return nil, err
	}
	var out []Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	// the sender numbers an album's pictures; several share a second
	index := func(m Message) int32 {
		c, _ := m.Content()
		return c.GetMessageContextInfo().GetMessageAssociation().GetMessageIndex()
	}
	sort.SliceStable(out, func(i, j int) bool { return index(out[i]) < index(out[j]) })
	return out, r.facts(ctx, out)
}

// defaultPinSecs is how long whatsapp keeps a pin that names no duration.
const defaultPinSecs = 7 * 24 * 60 * 60

// Starred is every starred message, newest first, before cursor.
func (r *Reader) Starred(ctx context.Context, addrs []string, from Cursor, limit int) ([]Message, error) {
	if from == (Cursor{}) {
		from = Cursor{T: tMax}
	}
	// from the stars, a few dozen, not through every message
	q := `SELECT m.rowid AS r, m.t, m.id FROM msg m
		WHERE m.rowid IN (SELECT m2.rowid FROM appstate s CROSS JOIN msg m2 ON m2.id = s.b
			WHERE s.kind = 'star' AND s.op = 'set' AND s.on_ = 1)
		AND (m.t, m.ord, m.id) < (?, ?, ?)`
	args := []any{from.T, from.Ord, from.ID}
	if len(addrs) > 0 {
		q += ` AND +m.chat IN (` + placeholders(len(addrs)) + `)`
		args = append(args, anys(addrs)...)
	}
	q += ` ORDER BY m.t DESC, m.ord DESC, m.id DESC LIMIT ?`
	return r.list(ctx, page(q, "DESC"), append(args, limit)...)
}

// Pinned is a chat's pinned messages, oldest pin first.
func (r *Reader) Pinned(ctx context.Context, addrs []string) ([]Message, error) {
	ph := placeholders(len(addrs))
	// from the pins, not through the whole chat
	ms, err := r.list(ctx, `SELECT `+msgCols+` FROM msg m
		WHERE +m.chat IN (`+ph+`) AND m.id IN (SELECT target FROM f_pin WHERE chat IN (`+ph+`))
		ORDER BY m.t, m.ord, m.id`, append(anys(addrs), anys(addrs)...)...)
	if err != nil {
		return nil, err
	}
	out := ms[:0]
	for _, m := range ms {
		if m.Facts.Pinned && !m.Facts.Revoked {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Facts.PinT < out[j].Facts.PinT })
	return out, nil
}

// Media is a chat's messages of the given field kinds, newest first.
func (r *Reader) Media(ctx context.Context, addrs []string, kinds []string, from Cursor, limit int) ([]Message, error) {
	if from == (Cursor{}) {
		from = Cursor{T: tMax}
	}
	// a proven delete already made the row a tombstone, which no kind or
	// text matches
	where := `m.kind IN (` + placeholders(len(kinds)) + `) AND (m.t, m.ord, m.id) < (?, ?, ?)`
	return r.list(ctx, byChat(len(addrs), where, "DESC"),
		byChatArgs(addrs, append(anys(kinds), from.T, from.Ord, from.ID), limit)...)
}

// Search finds messages whose words contain query, newest first; addrs
// narrows it to one chat.
func (r *Reader) Search(ctx context.Context, query string, addrs []string, from Cursor, limit int) ([]Message, error) {
	if from == (Cursor{}) {
		from = Cursor{T: tMax}
	}
	like := "%" + escapeLike(query) + "%"
	where := `m.text != '' AND m.text LIKE ? ESCAPE '\' AND (m.t, m.ord, m.id) < (?, ?, ?)`
	args := []any{like, from.T, from.Ord, from.ID}
	rowids, ok, err := r.searchCandidates(ctx, query)
	if err != nil {
		return nil, err
	}
	if ok {
		q := `SELECT m.rowid AS r, m.t, m.id FROM json_each(?) j CROSS JOIN msg m ON m.rowid = j.value WHERE ` + where
		args = append([]any{rowids}, args...)
		if len(addrs) > 0 {
			q += ` AND m.chat IN (` + placeholders(len(addrs)) + `)`
			args = append(args, anys(addrs)...)
		}
		q += ` ORDER BY m.t DESC, m.ord DESC, m.id DESC LIMIT ?`
		return r.list(ctx, page(q, "DESC"), append(args, limit)...)
	}
	// the word is common or short: its matches are dense, the newest
	// messages hold a page of them
	if len(addrs) > 0 {
		return r.list(ctx, byChat(len(addrs), where, "DESC"), byChatArgs(addrs, args, limit)...)
	}
	q := `SELECT m.rowid AS r, m.t, m.id FROM msg m WHERE ` + where
	if query != "" {
		// an empty query matches empty texts too, which the index leaves out
		q = `SELECT m.rowid AS r, m.t, m.id FROM msg m INDEXED BY msg_recent WHERE m.text != '' AND ` + where
	}
	q += ` ORDER BY m.t DESC, m.ord DESC, m.id DESC LIMIT ?`
	return r.list(ctx, page(q, "DESC"), append(args, limit)...)
}

// searchMax is how many rows holding every three letters of a query a
// search checks one by one; past it the query is common enough to walk.
var searchMax = 4096

// searchCandidates is every row whose text has all of the query's three
// letter runs, a superset of its LIKE matches, as a json list; not ok when
// the query is under three letters or more rows than searchMax have them.
func (r *Reader) searchCandidates(ctx context.Context, query string) (string, bool, error) {
	match := trigrams(query)
	if match == "" {
		return "", false, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT rowid FROM msg_text WHERE msg_text MATCH ? LIMIT ?`, match, searchMax+1)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 64)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return "", false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if len(ids) > searchMax {
		return "", false, nil
	}
	b, err := json.Marshal(ids)
	return string(b), true, err
}

// trigrams is the fts5 query for every three letter run of s, "" when it
// has none. the index folds case the way it folds the text.
func trigrams(s string) string {
	if !utf8.ValidString(s) {
		return ""
	}
	rs := []rune(s)
	if len(rs) < 3 {
		return ""
	}
	seen := map[string]bool{}
	var parts []string
	for i := 0; i+3 <= len(rs); i++ {
		t := string(rs[i : i+3])
		if seen[t] {
			continue
		}
		seen[t] = true
		parts = append(parts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " AND ")
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (r *Reader) list(ctx context.Context, q string, args ...any) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []Message
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out = dedupe(out)
	return out, r.facts(ctx, out)
}

// MessageChat is the address a message id lives under, for ids that come
// without their chat.
func (r *Reader) MessageChat(ctx context.Context, id string) (string, bool, error) {
	var chat string
	err := r.db.QueryRowContext(ctx, `SELECT chat FROM msg WHERE id = ? UNION SELECT chat FROM msg_wait WHERE id = ?
		UNION SELECT chat FROM outbox WHERE id = ? AND queued_t > 0 LIMIT 1`, id, id, id).Scan(&chat)
	if isNoRows(err) {
		return "", false, nil
	}
	return chat, err == nil, err
}

// Homes is the address each of ids is named by under addrs, for the ones
// that are here.
func (r *Reader) Homes(ctx context.Context, addrs, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(addrs) == 0 || len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, chat FROM msg WHERE id IN (`+placeholders(len(ids))+`)
		AND chat IN (`+placeholders(len(addrs))+`) ORDER BY rowid DESC`, append(anys(ids), anys(addrs)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, chat string
		if err := rows.Scan(&id, &chat); err != nil {
			return nil, err
		}
		// the oldest copy comes last and stays
		out[id] = chat
	}
	return out, rows.Err()
}

// Unseen is what came into addrs up to and with upTo that we have not read
// here or anywhere, oldest first, at most limit of the newest.
func (r *Reader) Unseen(ctx context.Context, addrs []string, upTo Cursor, limit int) ([]Message, error) {
	in := jsonList(addrs)
	from, err := r.readTo(ctx, in)
	if err != nil {
		return nil, err
	}
	ms, err := r.list(ctx, `SELECT `+msgCols+` FROM msg m
		WHERE m.chat IN (SELECT value FROM json_each(?)) AND m.from_me = 0 AND m.kind NOT LIKE 'stub:%'
		AND (m.t, m.ord) > (?, ?) AND (m.t, m.ord, m.id) <= (?, ?, ?)
		ORDER BY m.t DESC, m.ord DESC, m.id DESC LIMIT ?`, in, from.T, from.Ord, upTo.T, upTo.Ord, upTo.ID, limit)
	if err != nil {
		return nil, err
	}
	ms = dedupe(ms)
	slices.Reverse(ms)
	return ms, nil
}
