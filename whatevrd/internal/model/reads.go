package model

import (
	"context"
	"database/sql"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nyaruka/phonenumbers"
	"go.mau.fi/whatsmeow/types"
)

// Reader answers questions over the folded tables. it never writes; the
// clock is only for what has no folded answer, like a mute running out.
type Reader struct {
	db  querier
	now func() time.Time
}

// querier is the read pool, or a fold's transaction for the reads a fold
// sums up.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func NewReader(db *sql.DB) *Reader { return &Reader{db: db, now: time.Now} }

// World is identity as of one read: who is whom, and what each is called.
type World struct {
	// owners of each pn that more than one lid had, in time, oldest first
	owners shards[[]span]
	// one is the lid of each pn only one lid ever had: id_owner says the
	// same in a row of its own, which is half of all identity to read
	one shards[string]
	// pns of each lid, newest first
	pns      shards[[]string]
	self     map[string]bool
	names    shards[map[string]string]
	contacts shards[string]
	selfName string
}

type span struct {
	from, to int64
	lid      string
}

// World reads identity whole: a few thousand rows, cheaper than asking per
// row.
func (r *Reader) World(ctx context.Context) (*World, error) {
	w := &World{self: map[string]bool{}}
	rows, err := r.db.QueryContext(ctx, `SELECT lid, pn FROM id_map ORDER BY lid, since DESC, pn`)
	if err != nil {
		return nil, err
	}
	var many []string
	for rows.Next() {
		var lid, pn string
		if err := rows.Scan(&lid, &pn); err != nil {
			rows.Close()
			return nil, err
		}
		w.pns.set(lid, append(w.pns.get(lid), pn))
		if _, ok := w.one.lookup(pn); ok {
			many = append(many, pn)
		}
		w.one.set(pn, lid)
	}
	rows.Close()
	for _, pn := range many {
		w.one.del(pn)
	}
	if len(many) > 0 {
		rows, err = r.db.QueryContext(ctx, `SELECT pn, from_t, to_t, lid FROM id_owner
			WHERE pn IN (SELECT pn FROM id_map GROUP BY pn HAVING COUNT(*) > 1) ORDER BY pn, from_t`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var pn string
			var s span
			if err := rows.Scan(&pn, &s.from, &s.to, &s.lid); err != nil {
				rows.Close()
				return nil, err
			}
			w.owners.set(pn, append(w.owners.get(pn), s))
		}
		rows.Close()
	}
	rows, err = r.db.QueryContext(ctx, `SELECT jid FROM id_self`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var j string
		if rows.Scan(&j) == nil {
			w.self[j] = true
		}
	}
	rows.Close()
	rows, err = r.db.QueryContext(ctx, `SELECT jid, source, name FROM id_name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var j, src, name string
		if err := rows.Scan(&j, &src, &name); err != nil {
			rows.Close()
			return nil, err
		}
		ns := w.names.get(j)
		if ns == nil {
			ns = map[string]string{}
			w.names.set(j, ns)
		}
		ns[src] = name
	}
	rows.Close()
	// contact names are app state: the newest per index, removed ones gone
	rows, err = r.db.QueryContext(ctx, `SELECT kind, a, s, s2, op FROM appstate WHERE kind IN (?, ?, ?) ORDER BY rowid`, asContact, asLIDContact, asPushName)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, a, s, s2, op string
		if err := rows.Scan(&kind, &a, &s, &s2, &op); err != nil {
			rows.Close()
			return nil, err
		}
		switch {
		case kind == asPushName:
			w.selfName = s
		case op == "set":
			w.contacts.set(a, s)
			if s2 != "" {
				ns := w.names.get(a)
				if ns == nil {
					ns = map[string]string{}
					w.names.set(a, ns)
				}
				if ns[NameUsername] == "" {
					ns[NameUsername] = s2
				}
			}
		default:
			// a removed contact still counts as "no name", not as missing
			if _, ok := w.contacts.lookup(a); !ok {
				w.contacts.set(a, "")
			}
		}
	}
	rows.Close()
	return w, rows.Err()
}

// Patch is w with persons read again, the ids a fold touched: equal to a new
// World, for a fraction of one when few people changed. w is not changed.
func (r *Reader) Patch(ctx context.Context, w *World, persons []string) (*World, error) {
	n := &World{owners: w.owners.copy(), one: w.one.copy(), pns: w.pns.copy(), self: w.self,
		names: w.names.copy(), contacts: w.contacts.copy(), selfName: w.selfName}
	lids, pns := map[string]bool{}, map[string]bool{}
	for _, p := range persons {
		if p == "self" {
			self, err := strs(ctx, r.db, `SELECT jid FROM id_self`)
			if err != nil {
				return nil, err
			}
			n.self = map[string]bool{}
			for _, j := range self {
				n.self[j] = true
			}
			n.selfName = ""
			if err := r.db.QueryRowContext(ctx, `SELECT s FROM appstate WHERE kind = ? ORDER BY rowid DESC LIMIT 1`, asPushName).Scan(&n.selfName); err != nil && !isNoRows(err) {
				return nil, err
			}
			continue
		}
		switch {
		case isLID(p):
			lids[p] = true
		case isPN(p):
			pns[p] = true
			more, err := strs(ctx, r.db, `SELECT lid FROM id_map WHERE pn = ?`, p)
			if err != nil {
				return nil, err
			}
			for _, l := range more {
				lids[l] = true
			}
		}
		if err := r.patchNames(ctx, n, p); err != nil {
			return nil, err
		}
	}
	for l := range lids {
		got, err := strs(ctx, r.db, `SELECT pn FROM id_map WHERE lid = ? ORDER BY since DESC, pn`, l)
		if err != nil {
			return nil, err
		}
		if len(got) == 0 {
			n.pns.del(l)
		} else {
			n.pns.set(l, got)
		}
		for _, pn := range got {
			pns[pn] = true
		}
	}
	for pn := range pns {
		owners, err := strs(ctx, r.db, `SELECT lid FROM id_map WHERE pn = ?`, pn)
		if err != nil {
			return nil, err
		}
		n.one.del(pn)
		n.owners.del(pn)
		switch len(owners) {
		case 0:
		case 1:
			n.one.set(pn, owners[0])
		default:
			rows, err := r.db.QueryContext(ctx, `SELECT from_t, to_t, lid FROM id_owner WHERE pn = ? ORDER BY from_t`, pn)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var s span
				if err := rows.Scan(&s.from, &s.to, &s.lid); err != nil {
					rows.Close()
					return nil, err
				}
				n.owners.set(pn, append(n.owners.get(pn), s))
			}
			rows.Close()
		}
	}
	return n, nil
}

// patchNames reads again every name of one address, as World does.
func (r *Reader) patchNames(ctx context.Context, w *World, j string) error {
	w.names.del(j)
	w.contacts.del(j)
	rows, err := r.db.QueryContext(ctx, `SELECT source, name FROM id_name WHERE jid = ?`, j)
	if err != nil {
		return err
	}
	for rows.Next() {
		var src, name string
		if err := rows.Scan(&src, &name); err != nil {
			rows.Close()
			return err
		}
		ns := w.names.get(j)
		if ns == nil {
			ns = map[string]string{}
			w.names.set(j, ns)
		}
		ns[src] = name
	}
	rows.Close()
	rows, err = r.db.QueryContext(ctx, `SELECT s, s2, op FROM appstate WHERE kind IN (?, ?) AND a = ? ORDER BY rowid`, asContact, asLIDContact, j)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s, s2, op string
		if err := rows.Scan(&s, &s2, &op); err != nil {
			return err
		}
		if op != "set" {
			if _, ok := w.contacts.lookup(j); !ok {
				w.contacts.set(j, "")
			}
			continue
		}
		w.contacts.set(j, s)
		if s2 != "" {
			// a copy: the old world still holds the map it had
			names := maps.Clone(w.names.get(j))
			if names == nil {
				names = map[string]string{}
			}
			if names[NameUsername] == "" {
				names[NameUsername] = s2
			}
			w.names.set(j, names)
		}
	}
	return rows.Err()
}

// Key is the person or chat an address belongs to at time t: the lid when
// one is known, the address itself otherwise. groups and the rest are their
// own key.
func (w *World) Key(addr string, t int64) string {
	if !isPN(addr) {
		return addr
	}
	if lid, ok := w.one.lookup(addr); ok {
		return lid
	}
	spans := w.owners.get(addr)
	for _, s := range spans {
		if t >= s.from && t < s.to {
			return s.lid
		}
	}
	if len(spans) > 0 {
		return spans[len(spans)-1].lid
	}
	return addr
}

// Now is the key an address belongs to today.
func (w *World) Now(addr string) string { return w.Key(addr, tMax-1) }

// PN is the newest phone number of a key, "" for none.
func (w *World) PN(key string) string {
	if isPN(key) {
		return key
	}
	if pns := w.pns.get(key); len(pns) > 0 {
		return pns[0]
	}
	return ""
}

// LID is the lid of a key, "" for none.
func (w *World) LID(key string) string {
	if isLID(key) {
		return key
	}
	if isPN(key) {
		if k := w.Now(key); isLID(k) {
			return k
		}
	}
	return ""
}

// Addrs is every address that folds into key: the key and its numbers.
func (w *World) Addrs(key string) []string {
	out := []string{key}
	if isLID(key) {
		out = append(out, w.pns.get(key)...)
	}
	return out
}

// IsSelf says an address is this account.
func (w *World) IsSelf(addr string) bool {
	if addr == Me || w.self[addr] {
		return true
	}
	k := w.Now(addr)
	return w.self[k]
}

func (w *World) SelfName() string { return w.selfName }

// SelfPN is this account's phone number address.
func (w *World) SelfPN() string {
	for j := range w.self {
		if isPN(j) {
			return j
		}
	}
	return ""
}

// Name is what to call a person, best source first: contact, verified
// business, push name, username, the number. the push name gets the tilde
// whatsapp gives a name the person chose for themselves.
func (w *World) Name(key string) (name, source string) {
	addrs := w.Addrs(key)
	var buf [4]map[string]string
	names := buf[:0]
	for _, a := range addrs {
		if n := strings.TrimSpace(w.contacts.get(a)); n != "" {
			return n, NameContact
		}
		names = append(names, w.names.get(a))
	}
	for _, ns := range names {
		if n := strings.TrimSpace(ns[NameInline]); n != "" {
			return n, NameContact
		}
	}
	for _, src := range []string{NameBusiness, NamePush, NameUsername} {
		for _, ns := range names {
			if n := strings.TrimSpace(ns[src]); n != "" {
				// whatsmeow drops the verified level, so a business name is
				// only what the account calls itself, same as a push name
				if src == NamePush || src == NameBusiness {
					n = "~" + strings.TrimPrefix(n, "~")
				}
				return n, src
			}
		}
	}
	if pn := w.PN(key); pn != "" {
		return FormatPhone(pn), "phone"
	}
	return "", ""
}

// Names is each name key goes by, by where it came from: the saved contact,
// the push name and the business name, untouched.
func (w *World) Names(key string) (saved, push, business string) {
	for _, a := range w.Addrs(key) {
		ns := w.names.get(a)
		if saved == "" {
			saved = strings.TrimSpace(w.contacts.get(a))
		}
		if saved == "" {
			saved = strings.TrimSpace(ns[NameInline])
		}
		if push == "" {
			push = strings.TrimSpace(ns[NamePush])
		}
		if business == "" {
			business = strings.TrimSpace(ns[NameBusiness])
		}
	}
	return saved, push, business
}

// Username is the key of whoever goes by username u, "" for nobody.
func (w *World) Username(u string) string {
	u = strings.TrimPrefix(strings.TrimSpace(u), "@")
	if u == "" {
		return ""
	}
	for a, ns := range w.names.all() {
		if strings.EqualFold(ns[NameUsername], u) {
			return w.Now(a)
		}
	}
	return ""
}

// FormatPhone is a number written the way its country writes it.
func FormatPhone(pn string) string {
	if v, ok := phones.Load(pn); ok {
		return v.(string)
	}
	out := formatPhone(pn)
	phones.Store(pn, out)
	return out
}

// phones remembers formatted numbers: parsing one costs more than the rest
// of a chat row, and the list formats every number on every read.
var phones sync.Map

func formatPhone(pn string) string {
	j, err := types.ParseJID(pn)
	if err != nil || j.User == "" {
		return ""
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, j.User)
	if digits == "" {
		return ""
	}
	num, err := phonenumbers.Parse("+"+digits, "ZZ")
	if err != nil || !phonenumbers.IsValidNumber(num) {
		return "+" + digits
	}
	return phonenumbers.Format(num, phonenumbers.INTERNATIONAL)
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anys(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Chat is a chat row as reads see it: everything under one key.
type Chat struct {
	Key      string
	PN, LID  string
	Group    bool
	Addrs    []string
	Name     string
	NameFrom string
	LastT    int64
	Pinned   bool
	PinT     int64
	Archived bool
	Muted    bool
	MuteEnd  int64
	Unread   int
	// MarkedUnread is the phone's "mark as unread", no count behind it
	MarkedUnread bool
	Exhausted    bool
	Ephemeral    int
	// GroupError is why the server would not describe the group
	GroupError string
	ReadOnly   bool

	// what the name is made from at read time
	grpName, histName string
	// deleted is a chat the phone deleted with nothing newer since
	deleted bool
}

type ChatFilter struct {
	// Kind is "", "direct" or "groups"
	Kind     string
	Archived bool
	// Any takes archived and unarchived alike
	Any   bool
	Limit int
	// Offset skips that many rows first
	Offset int
	// Ties goes on past Limit through the rows in the same second as the
	// last, for a caller that orders by the second and cuts after
	Ties bool
	// Name keeps chats whose name holds it, any case
	Name string
}

// chatState is the per address state a chat row is put together from.
type chatState struct {
	last    int64
	hist    *histChat
	as      map[string]asRow
	grpErr  string
	grpName string
}

type histChat struct {
	t                          int64
	name                       string
	archived, markedUnread, ro bool
	pinned, muteEnd            int64
	unread, ephemeral, endType int
}

type asRow struct {
	on   bool
	n, t int64
	s    string
}

// Chats is the chat list: pinned first, newest pin first, then newest
// message first.
func (r *Reader) Chats(ctx context.Context, f ChatFilter) ([]Chat, error) {
	w, err := r.World(ctx)
	if err != nil {
		return nil, err
	}
	return r.ChatsIn(ctx, w, f)
}

// ChatsIn is Chats as w has it, for a caller that keeps its world.
func (r *Reader) ChatsIn(ctx context.Context, w *World, f ChatFilter) ([]Chat, error) {
	where, args := f.where()
	q := `SELECT ` + rowCols + ` FROM chat_row WHERE ` + where + ` ORDER BY pinned DESC, pin_t DESC, last_t DESC, key`
	name := strings.ToLower(strings.TrimSpace(f.Name))
	switch {
	case f.Limit > 0 && name == "" && !f.Ties:
		q += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	case f.Offset > 0:
		q += ` LIMIT -1 OFFSET ?`
		args = append(args, f.Offset)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := r.now().UnixMilli()
	var out []Chat
	for rows.Next() {
		c, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		dress(w, &c, now)
		if name != "" && !strings.Contains(strings.ToLower(c.Name), name) {
			continue
		}
		if f.Limit > 0 && len(out) >= f.Limit && (!f.Ties || second(c) != second(out[len(out)-1])) {
			break
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// where is the filter as a WHERE clause on chat_row, without Name.
func (f ChatFilter) where() (string, []any) {
	q := `deleted = 0`
	var args []any
	if !f.Any {
		q += ` AND archived = ?`
		args = append(args, f.Archived)
	}
	switch f.Kind {
	case "direct":
		q += ` AND grp = 0`
	case "groups":
		q += ` AND grp = 1`
	}
	return q, args
}

// ChatCount is how many chats f takes, Name and Limit aside.
func (r *Reader) ChatCount(ctx context.Context, f ChatFilter) (int, error) {
	where, args := f.where()
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM chat_row WHERE `+where, args...).Scan(&n)
	return n, err
}

// ChatsBefore is how many chats f takes that the list shows above the
// position (pinned, pinT, lastT, key).
func (r *Reader) ChatsBefore(ctx context.Context, f ChatFilter, pinned bool, pinT, lastT int64, key string) (int, error) {
	where, args := f.where()
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM chat_row WHERE `+where+`
		AND (pinned > ? OR pinned = ? AND (pin_t > ? OR pin_t = ? AND (last_t > ? OR last_t = ? AND key < ?)))`,
		append(args, pinned, pinned, pinT, pinT, lastT, lastT, key)...).Scan(&n)
	return n, err
}

// second is where a chat sorts to the second.
func second(c Chat) [2]int64 {
	var pin int64
	if c.Pinned {
		pin = 1 + c.PinT/1000
	}
	return [2]int64{pin, c.LastT / 1000}
}

// Chat is one chat by any of its addresses, with its unread count.
func (r *Reader) Chat(ctx context.Context, addr string) (Chat, bool, error) {
	w, err := r.World(ctx)
	if err != nil {
		return Chat{}, false, err
	}
	return r.ChatIn(ctx, w, addr)
}

// ChatIn is Chat as w has it. a chat with nothing in it yet is still a chat,
// with every address it could hear from; one the phone deleted is not.
func (r *Reader) ChatIn(ctx context.Context, w *World, addr string) (Chat, bool, error) {
	key := w.Now(user(addr))
	if key == "" {
		return Chat{}, false, nil
	}
	c, err := scanRow(r.db.QueryRowContext(ctx, `SELECT `+rowCols+` FROM chat_row WHERE key = ?`, key))
	switch {
	case isNoRows(err):
		c = Chat{Key: key, Group: server(key) == types.GroupServer}
	case err != nil:
		return Chat{}, false, err
	case c.deleted:
		return Chat{}, false, nil
	}
	// an address nothing came under yet goes where its first message would
	idle := w.Addrs(key)
	rows, err := r.db.QueryContext(ctx, `SELECT addr FROM chat_addr WHERE addr IN (`+placeholders(len(idle))+`)`, anys(idle)...)
	if err != nil {
		return Chat{}, false, err
	}
	taken := map[string]bool{}
	for rows.Next() {
		var a string
		if rows.Scan(&a) == nil {
			taken[a] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Chat{}, false, err
	}
	for _, a := range idle {
		if !taken[a] && w.Key(a, 0) == key {
			c.Addrs = append(c.Addrs, a)
		}
	}
	sort.Strings(c.Addrs)
	dress(w, &c, r.now().UnixMilli())
	return c, true, nil
}

const rowCols = `key, addrs, grp, last_t, pinned, pin_t, archived, muted, mute_end, unread, marked_unread,
	exhausted, ephemeral, read_only, group_error, grp_name, hist_name, deleted`

func scanRow(row interface{ Scan(...any) error }) (Chat, error) {
	var c Chat
	var addrs string
	err := row.Scan(&c.Key, &addrs, &c.Group, &c.LastT, &c.Pinned, &c.PinT, &c.Archived, &c.Muted, &c.MuteEnd,
		&c.Unread, &c.MarkedUnread, &c.Exhausted, &c.Ephemeral, &c.ReadOnly, &c.GroupError, &c.grpName, &c.histName, &c.deleted)
	if addrs != "" {
		c.Addrs = strings.Split(addrs, ",")
	}
	return c, err
}

// dress adds to a stored row what depends on who is who and on the clock.
func dress(w *World, c *Chat, now int64) {
	c.PN, c.LID = w.PN(c.Key), w.LID(c.Key)
	if c.Muted && c.MuteEnd > 0 && muteEndMS(c.MuteEnd) < now {
		c.Muted, c.MuteEnd = false, 0
	}
	c.Name, c.NameFrom = chatName(w, c)
}

// muteEndMS takes a mute end in whichever unit it came: app state uses ms,
// history seconds.
func muteEndMS(t int64) int64 {
	if t > 0 && t < 1e11 {
		return t * 1000
	}
	return t
}

// UnknownGroup is a group nothing ever named: the server will not describe
// it and history had no subject. never a raw jid.
const UnknownGroup = "Unknown group"

func chatName(w *World, c *Chat) (string, string) {
	hist := strings.TrimSpace(c.histName)
	switch {
	case c.Group:
		if c.grpName != "" {
			return c.grpName, "group"
		}
		if hist != "" {
			return hist, "group"
		}
		return UnknownGroup, "group"
	case server(c.Key) == types.NewsletterServer || server(c.Key) == types.BroadcastServer:
		if c.histName != "" {
			return c.histName, "contact"
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
	if hist != "" {
		return hist, NameContact
	}
	// the list shows an unsaved number as the number, as whatsapp does
	if pn := w.PN(c.Key); pn != "" {
		return FormatPhone(pn), "phone"
	}
	return w.Name(c.Key)
}
