package model

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/core"
)

// Me stands for this account wherever a sender is stored: history does not
// say which of our addresses sent a message, and our addresses change.
const Me = "me"

// where a message body came from, the higher one wins when a message comes
// twice
const (
	srcHistory = 1
	srcLive    = 2
	srcExact   = 3
	srcSent    = 4
)

// messages holds one row per message and every fact one message states about
// another, keyed by target so the fact can come first.
//
// rows store only what reads filter and sort on. the rest of a message is
// decoded from its body in the log, found by seq (and off, len for a message
// inside a history conversation).
var messagesDomain = core.Domain{
	Name:    "messages",
	Version: 14,
	Tables: []string{"msg", "msg_src", "msg_wait", "msg_gone", "f_reaction", "f_edit", "f_revoke", "f_enc",
		"f_pin", "f_keep", "f_receipt", "f_ephemeral", "sys_msg", "f_seen", "msg_text", "msg_text_q", "live"},
	Schema: append([]string{
		`CREATE TABLE msg (
			chat       TEXT NOT NULL,
			id         TEXT NOT NULL,
			sender     TEXT NOT NULL,
			sender_alt TEXT NOT NULL DEFAULT '',
			from_me    INTEGER NOT NULL,
			t          INTEGER NOT NULL,
			kind       TEXT NOT NULL,
			text       TEXT NOT NULL DEFAULT '',
			reply      TEXT NOT NULL DEFAULT '',
			album      TEXT NOT NULL DEFAULT '',
			secret     BLOB,
			status     INTEGER NOT NULL DEFAULT 0,
			view_once  INTEGER NOT NULL DEFAULT 0,
			src        INTEGER NOT NULL,
			hash       BLOB NOT NULL,
			seq        INTEGER NOT NULL,
			off        INTEGER,
			len        INTEGER,
			-- where it came among messages that share its second, see arrival
			ord        INTEGER NOT NULL,
			-- the message's own bytes, so a read never loads the history
			-- conversation around it from the log
			body       BLOB,
			PRIMARY KEY (chat, id)
		)`,
		`CREATE INDEX msg_chat_t ON msg (chat, t, ord, id)`,
		`CREATE INDEX msg_id ON msg (id)`,
		`CREATE INDEX msg_album ON msg (album) WHERE album != ''`,
		// the album pictures a transcript hides, counted without the rest
		`CREATE INDEX msg_album_chat ON msg (chat, t, ord, id, album) WHERE album != ''`,
		// what unread counts and what moves its horizon, each a seek
		`CREATE INDEX msg_in ON msg (chat, t, ord) WHERE from_me = 0 AND kind NOT LIKE 'stub:%'`,
		`CREATE INDEX msg_out ON msg (chat, t, ord) WHERE from_me = 1`,
		// the stickers we sent are the picker's recents
		`CREATE INDEX msg_sticker ON msg (t) WHERE from_me = 1 AND kind = 'stickerMessage'`,
		// search: the newest texts first without reading bodies, and every
		// three letters of each text, which narrow a rare word to a few rows
		`CREATE INDEX msg_recent ON msg (t, ord, id, text) WHERE text != ''`,
		`CREATE VIRTUAL TABLE msg_text USING fts5 (text, content = 'msg', content_rowid = 'rowid',
			tokenize = 'trigram', detail = 'none', columnsize = 0)`,
		// rows whose text may have changed since the index last caught up, each
		// with its text before (NULL for a new row). the index takes them once
		// per commit: fts5 writes itself out at every savepoint, one per input
		`CREATE TABLE msg_text_q (rid INTEGER NOT NULL, old TEXT)`,
		`CREATE TRIGGER msg_text_in AFTER INSERT ON msg WHEN NEW.text != '' BEGIN
			INSERT INTO msg_text_q (rid, old) VALUES (NEW.rowid, NULL);
		END`,
		`CREATE TRIGGER msg_text_change AFTER UPDATE OF text ON msg WHEN NEW.text IS NOT OLD.text BEGIN
			INSERT INTO msg_text_q (rid, old) VALUES (OLD.rowid, OLD.text);
		END`,
		`CREATE TRIGGER msg_text_out AFTER DELETE ON msg WHEN OLD.text != '' BEGIN
			INSERT INTO msg_text_q (rid, old) VALUES (OLD.rowid, OLD.text);
		END`,
		// every body a message came in, so a delete can scrub them all, and who
		// sent it, which a scrubbed body no longer says
		`CREATE TABLE msg_src (
			chat   TEXT NOT NULL,
			id     TEXT NOT NULL,
			t      INTEGER NOT NULL,
			seq    INTEGER NOT NULL,
			off    INTEGER NOT NULL DEFAULT -1,
			len    INTEGER NOT NULL DEFAULT -1,
			sender TEXT NOT NULL DEFAULT '',
			alt    TEXT NOT NULL DEFAULT '',
			ord    INTEGER NOT NULL,
			PRIMARY KEY (chat, id, seq, off)
		) WITHOUT ROWID`,
		// messages a delete took, so what is said about them later is dropped too
		`CREATE TABLE msg_gone (
			chat TEXT NOT NULL,
			id   TEXT NOT NULL,
			PRIMARY KEY (chat, id)
		)`,
		`CREATE INDEX msg_gone_id ON msg_gone (id)`,
		`CREATE INDEX msg_src_id ON msg_src (id)`,
		`CREATE TABLE msg_wait (
			chat        TEXT NOT NULL,
			id          TEXT NOT NULL,
			sender      TEXT NOT NULL,
			from_me     INTEGER NOT NULL,
			t           INTEGER NOT NULL,
			ord         INTEGER NOT NULL,
			unavailable TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (chat, id)
		)`,
		`CREATE INDEX msg_wait_id ON msg_wait (id)`,
		`CREATE TABLE f_reaction (
			chat   TEXT NOT NULL,
			target TEXT NOT NULL,
			sender TEXT NOT NULL,
			emoji  TEXT NOT NULL,
			t      INTEGER NOT NULL,
			PRIMARY KEY (chat, target, sender)
		)`,
		`CREATE INDEX f_reaction_target ON f_reaction (target)`,
		// facts about another's message are kept per sender, so one that may
		// not say it never pushes out one that may. who may is decided where
		// they are read, against the message's author and the group
		`CREATE TABLE f_edit (
			chat   TEXT NOT NULL,
			target TEXT NOT NULL,
			by     TEXT NOT NULL,
			by_alt TEXT NOT NULL DEFAULT '',
			t      INTEGER NOT NULL,
			hash   BLOB NOT NULL,
			body   BLOB NOT NULL,
			seq    INTEGER NOT NULL,
			PRIMARY KEY (chat, target, by)
		)`,
		`CREATE INDEX f_edit_target ON f_edit (target)`,
		// ok once the revoke is proven, see revoke.go
		`CREATE TABLE f_revoke (
			chat   TEXT NOT NULL,
			target TEXT NOT NULL,
			by     TEXT NOT NULL,
			by_alt TEXT NOT NULL DEFAULT '',
			t      INTEGER NOT NULL,
			ok     INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat, target, by)
		)`,
		`CREATE INDEX f_revoke_target ON f_revoke (target)`,
		`CREATE INDEX f_revoke_waiting ON f_revoke (chat) WHERE ok = 0`,
		// a vote, an event response or a reaction, sealed with the target's
		// message secret. it opens once the target is here; plain holds it.
		`CREATE TABLE f_enc (
			chat       TEXT NOT NULL,
			target     TEXT NOT NULL,
			sender     TEXT NOT NULL,
			use        TEXT NOT NULL,
			t          INTEGER NOT NULL,
			hash       BLOB NOT NULL,
			mod_jids   TEXT NOT NULL DEFAULT '',
			orig_jid   TEXT NOT NULL DEFAULT '',
			iv         BLOB,
			payload    BLOB,
			plain      BLOB,
			PRIMARY KEY (chat, target, sender, use)
		)`,
		`CREATE INDEX f_enc_target ON f_enc (target)`,
		// secs is how long the pin lasts, 0 when the pin did not say. phone is
		// set for what history carried, which the phone already checked
		`CREATE TABLE f_pin (
			chat   TEXT NOT NULL,
			target TEXT NOT NULL,
			by     TEXT NOT NULL,
			by_alt TEXT NOT NULL DEFAULT '',
			pinned INTEGER NOT NULL,
			t      INTEGER NOT NULL,
			secs   INTEGER NOT NULL DEFAULT 0,
			phone  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat, target, by)
		)`,
		`CREATE INDEX f_pin_target ON f_pin (target)`,
		`CREATE TABLE f_keep (
			chat   TEXT NOT NULL,
			target TEXT NOT NULL,
			by     TEXT NOT NULL,
			by_alt TEXT NOT NULL DEFAULT '',
			keep   INTEGER NOT NULL,
			t      INTEGER NOT NULL,
			phone  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat, target, by)
		)`,
		`CREATE INDEX f_keep_target ON f_keep (target)`,
		// the first time each recipient got to each state
		`CREATE TABLE f_receipt (
			chat TEXT NOT NULL,
			id   TEXT NOT NULL,
			who  TEXT NOT NULL,
			type TEXT NOT NULL,
			t    INTEGER NOT NULL,
			PRIMARY KEY (id, chat, who, type)
		) WITHOUT ROWID`,
		`CREATE TABLE f_ephemeral (
			chat  TEXT PRIMARY KEY,
			timer INTEGER NOT NULL,
			t     INTEGER NOT NULL
		)`,
		sysSchema,
		`CREATE INDEX sys_msg_chat_t ON sys_msg (chat, t, ord, id)`,
		// the newest message this account read, per address the read came
		// under and address the message is under. the triggers keep it equal
		// to the join of msg and f_receipt it stands for, in any order.
		`CREATE TABLE f_seen (
			mchat TEXT NOT NULL,
			rchat TEXT NOT NULL,
			t     INTEGER NOT NULL,
			ord   INTEGER NOT NULL,
			PRIMARY KEY (mchat, rchat)
		) WITHOUT ROWID`,
		`CREATE TRIGGER f_seen_receipt AFTER INSERT ON f_receipt WHEN ` + ownRead("NEW") + ` BEGIN
			INSERT INTO f_seen (mchat, rchat, t, ord) SELECT m.chat, NEW.chat, m.t, m.ord FROM msg m WHERE m.id = NEW.id
			` + seenNewer + `;
		END`,
		`CREATE TRIGGER f_seen_msg AFTER INSERT ON msg BEGIN ` + seenMsg + ` END`,
		`CREATE TRIGGER f_seen_later AFTER UPDATE OF t, ord ON msg WHEN (NEW.t, NEW.ord) > (OLD.t, OLD.ord) BEGIN ` + seenMsg + ` END`,
		// the newest read one went earlier or went away: count that chat again
		`CREATE TRIGGER f_seen_earlier AFTER UPDATE OF t, ord ON msg WHEN (NEW.t, NEW.ord) < (OLD.t, OLD.ord)
			AND EXISTS (SELECT 1 FROM f_seen WHERE mchat = OLD.chat AND t = OLD.t AND ord = OLD.ord) BEGIN ` + seenAgain + ` END`,
		`CREATE TRIGGER f_seen_gone AFTER DELETE ON msg
			WHEN EXISTS (SELECT 1 FROM f_seen WHERE mchat = OLD.chat AND t = OLD.t AND ord = OLD.ord) BEGIN ` + seenAgain + ` END`,
	}, liveSchema...),
	Folds: map[string]core.FoldFunc{
		core.KindMessage:             foldMessage,
		core.KindUndecryptable:       foldUndecryptable,
		core.KindReceipt:             foldReceipt,
		core.KindHistoryConversation: foldHistoryConversation,
		core.KindGroupInfo:           foldGroupSystem,
		core.KindPicture:             foldPictureSystem,
		core.KindIdentityChange:      foldIdentitySystem,
	},
	Finish: indexText,
}

// indexText brings the trigram index up to the rows queued since the last
// commit: what it held for each (the text before the first change, unless
// that change made the row) comes out, what the row says now goes in.
func indexText(tx *core.Tx, _ map[string][]string, _ map[string]bool) error {
	for _, q := range indexTextSQL {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

var indexTextSQL = []string{
	`INSERT INTO msg_text (msg_text, rowid, text)
		SELECT 'delete', q.rid, q.old FROM msg_text_q q
		WHERE q.rowid IN (SELECT MIN(rowid) FROM msg_text_q GROUP BY rid) AND q.old != ''`,
	`INSERT INTO msg_text (rowid, text)
		SELECT rowid, text FROM msg WHERE rowid IN (SELECT rid FROM msg_text_q) AND text != ''`,
	`DELETE FROM msg_text_q`,
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// known is who sent r, for sameChats: Me for a message of ours.
func (r *row) known() []string {
	if r.fromMe {
		return []string{Me}
	}
	return []string{r.sender, r.senderAlt}
}

// row is one message as the fold stores it.
type row struct {
	chat, id, sender, senderAlt string
	fromMe                      bool
	t                           int64
	kind, text, reply, album    string
	secret                      []byte
	status                      int
	viewOnce                    bool
	src                         int
	hash                        []byte
	seq                         int64
	off, len                    int
	// ord is where it arrived, see arrival
	ord int64
	// raw is the sender's own address when sender is Me
	raw string
	// conv is the history conversation a message came in, nil for a live one
	conv *conv
	// body is the message's own bytes: the input body, or its slice of a
	// history conversation
	body []byte
	// hidden is a live update that is a point of an earlier row's share
	hidden bool
}

// conv is what the messages of one history conversation share while they
// fold: nothing else folds in between, so it is read and written once.
type conv struct {
	clears []clear
	// names is each sender's newest push name in the conversation
	names map[string]stampedName
}

type stampedName struct {
	name string
	t    int64
}

func (r row) key() string { return r.chat + ":" + r.id }

func foldMessage(tx *core.Tx, in core.Input) error {
	h, err := head[core.MessageHead](in)
	if err != nil {
		return err
	}
	chat := user(h.Chat)
	if chat == "" || h.ID == "" {
		return nil
	}
	sender := user(h.Sender)
	if h.FromMe {
		sender = Me
	}
	if len(in.Body) == 0 {
		// scrubbed: only the delete that did it is left to agree with
		return putMsg(tx, row{chat: chat, id: h.ID, sender: sender, senderAlt: user(h.SenderAlt), t: ms(h.T, in), seq: in.Seq, off: -1, len: -1, ord: arrival(in.At, -1)})
	}
	var raw waE2E.Message
	if err := proto.Unmarshal(in.Body, &raw); err != nil {
		return err
	}
	src := srcLive
	switch {
	case h.Sent:
		src = srcSent
	case h.Exact:
		src = srcExact
	}
	sum := sha256.Sum256(in.Body)
	r := row{
		chat: chat, id: h.ID, sender: sender, senderAlt: user(h.SenderAlt), fromMe: h.FromMe, raw: user(h.Sender),
		t: ms(h.T, in), src: src, hash: sum[:], seq: in.Seq, off: -1, len: -1, ord: arrival(in.At, -1), body: in.Body,
	}
	return foldContent(tx, r, &raw, h.FromMe)
}

// foldContent sorts one message into a row or the fact it states.
func foldContent(tx *core.Tx, r row, raw *waE2E.Message, fromMe bool) error {
	u := Unwrap(raw)
	m := u.Msg
	if m == nil {
		return nil
	}
	f := fact{chat: r.chat, sender: r.sender, senderAlt: r.senderAlt, raw: r.raw, rawAlt: r.senderAlt, t: r.t, seq: r.seq, ord: r.ord}
	switch {
	case m.GetProtocolMessage() != nil:
		return foldProtocol(tx, f, m.GetProtocolMessage())
	case m.GetReactionMessage() != nil:
		x := m.GetReactionMessage()
		return putReaction(tx, f.at(x.GetSenderTimestampMS()), x.GetKey().GetID(), x.GetText())
	case m.GetEncReactionMessage() != nil:
		x := m.GetEncReactionMessage()
		return putEnc(tx, f, x.GetTargetMessageKey(), useReaction, x.GetEncIV(), x.GetEncPayload(), nil)
	case m.GetPollUpdateMessage() != nil:
		x := m.GetPollUpdateMessage()
		return putEnc(tx, f.at(x.GetSenderTimestampMS()), x.GetPollCreationMessageKey(), useVote,
			x.GetVote().GetEncIV(), x.GetVote().GetEncPayload(), nil)
	case m.GetEncEventResponseMessage() != nil:
		x := m.GetEncEventResponseMessage()
		return putEnc(tx, f, x.GetEventCreationMessageKey(), useEvent, x.GetEncIV(), x.GetEncPayload(), nil)
	case m.GetSecretEncryptedMessage() != nil:
		x := m.GetSecretEncryptedMessage()
		use := ""
		switch x.GetSecretEncType() {
		case waE2E.SecretEncryptedMessage_MESSAGE_EDIT:
			use = useEdit
		case waE2E.SecretEncryptedMessage_EVENT_EDIT:
			use = useEventEdit
		default:
			return nil
		}
		return putEnc(tx, f, x.GetTargetMessageKey(), use, x.GetEncIV(), x.GetEncPayload(), nil)
	case m.GetPinInChatMessage() != nil:
		x := m.GetPinInChatMessage()
		secs := max(m.GetMessageContextInfo().GetMessageAddOnDurationInSecs(), raw.GetMessageContextInfo().GetMessageAddOnDurationInSecs())
		return putPin(tx, f.at(x.GetSenderTimestampMS()), x.GetKey().GetID(), x.GetType() == waE2E.PinInChatMessage_PIN_FOR_ALL, secs, false)
	case m.GetKeepInChatMessage() != nil:
		x := m.GetKeepInChatMessage()
		return putKeep(tx, f.at(x.GetTimestampMS()), x.GetKey().GetID(), x.GetKeepType() == waE2E.KeepType_KEEP_FOR_ALL, false)
	}
	kind := Field(m)
	if kind == "" {
		return nil
	}
	r.kind = kind
	var err error
	if r.hidden, err = putLive(tx, r, m); err != nil {
		return err
	}
	r.viewOnce = u.ViewOnce
	r.text = searchText(m)
	r.reply = contextOf(m).GetStanzaID()
	r.album = m.GetMessageContextInfo().GetMessageAssociation().GetParentMessageKey().GetID()
	if r.secret == nil {
		r.secret = m.GetMessageContextInfo().GetMessageSecret()
	}
	return putMsg(tx, r)
}

// ownRead is the sql for "receipt r is this account reading": the phone's
// read-self, or a plain read sent with our own id.
func ownRead(r string) string {
	return `(` + r + `.type = 'read-self' OR ` + r + `.who = '` + Me + `' AND ` + r + `.type IN ('read', 'played', 'played-self'))`
}

var (
	// a read covers the message it names and what came before it, in the
	// transcript's order: same-second messages after it stay unread
	seenNewer = `ON CONFLICT (mchat, rchat) DO UPDATE SET t = excluded.t, ord = excluded.ord
		WHERE (excluded.t, excluded.ord) > (f_seen.t, f_seen.ord)`
	seenMsg = `INSERT INTO f_seen (mchat, rchat, t, ord) SELECT NEW.chat, f.chat, NEW.t, NEW.ord FROM f_receipt f
		WHERE f.id = NEW.id AND ` + ownRead("f") + ` ` + seenNewer + `;`
	seenAgain = `DELETE FROM f_seen WHERE mchat = OLD.chat;
		INSERT INTO f_seen (mchat, rchat, t, ord) SELECT mchat, rchat, t, ord FROM (
			SELECT m.chat AS mchat, f.chat AS rchat, m.t AS t, m.ord AS ord,
				ROW_NUMBER() OVER (PARTITION BY f.chat ORDER BY m.t DESC, m.ord DESC) AS n
			FROM msg m JOIN f_receipt f ON f.id = m.id AND ` + ownRead("f") + ` WHERE m.chat = OLD.chat)
		WHERE n = 1;`
)

// fact is who said something about another message, and when.
type fact struct {
	chat, sender, senderAlt string
	// raw and rawAlt are the sender's addresses even when it is us
	raw, rawAlt string
	t           int64
	seq         int64
	ord         int64
}

// known is who said it, as addresses of the chat it came under: in a chat
// with a person, they are that person or us
func (f fact) known() []string {
	if f.sender == Me {
		return nil
	}
	return []string{f.sender, f.senderAlt}
}

func (f fact) at(senderMS int64) fact {
	if senderMS > 0 {
		f.t = senderMS
	}
	return f
}

func foldProtocol(tx *core.Tx, f fact, p *waE2E.ProtocolMessage) error {
	target := p.GetKey().GetID()
	switch p.GetType() {
	case waE2E.ProtocolMessage_REVOKE:
		return putRevoke(tx, f, target)
	case waE2E.ProtocolMessage_MESSAGE_EDIT:
		body, err := marshal.Marshal(p.GetEditedMessage())
		if err != nil {
			return err
		}
		return putEdit(tx, f.at(p.GetTimestampMS()), target, body)
	case waE2E.ProtocolMessage_EPHEMERAL_SETTING:
		_, err := tx.Exec(`INSERT INTO f_ephemeral (chat, timer, t) VALUES (?, ?, ?)
			ON CONFLICT (chat) DO UPDATE SET timer = excluded.timer, t = excluded.t
			WHERE (excluded.t, excluded.timer) > (f_ephemeral.t, f_ephemeral.timer)`, f.chat, p.GetEphemeralExpiration(), f.t)
		tx.Touch("chat", f.chat)
		if err != nil || !isPerson(f.chat) {
			// a group's timer comes as a group notification
			return err
		}
		exp := p.GetEphemeralExpiration()
		return putSystem(tx, f.chat, f.t, f.ord, System{Type: "ephemeral", Actor: f.sender, On: exp > 0, Seconds: exp})
	}
	return nil
}

var marshal = proto.MarshalOptions{Deterministic: true}

// putMsg stores a message unless a delete already took it, keeping the
// better body when it came before. a row without a kind only records where
// the message was, for a delete to find.
func putMsg(tx *core.Tx, r row) error {
	if _, err := tx.Exec(`INSERT INTO msg_src (chat, id, t, seq, off, len, sender, alt, ord) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET sender = excluded.sender, alt = excluded.alt WHERE msg_src.sender = ''`,
		r.chat, r.id, r.t, r.seq, r.off, r.len, r.sender, r.senderAlt, r.ord); err != nil {
		return err
	}
	cs, err := sameChats(tx, r.chat, r.id, r.known()...)
	if err != nil {
		return err
	}
	var clears *[]clear
	if r.conv != nil {
		clears = &r.conv.clears
	}
	if gone, err := isGone(tx, cs, r.chat, r.id, r.t, clears); err != nil || gone {
		if err != nil {
			return err
		}
		if err := scrubBody(tx, r.seq, r.off, r.len); err != nil {
			return err
		}
		return dropMessage(tx, r.chat, r.id)
	}
	if revoked, err := checkRevokes(tx, cs, r.id); err != nil || revoked {
		return err
	}
	if r.kind == "" || r.hidden {
		return nil
	}
	var src int
	var hash []byte
	switch err := tx.QueryRow(`SELECT src, hash FROM msg WHERE chat = ? AND id = ?`, r.chat, r.id).Scan(&src, &hash); {
	case err == nil:
		if r.src < src || r.src == src && bytes.Compare(r.hash, hash) <= 0 {
			// the history status of an outgoing message still counts
			if r.status > 0 {
				if _, err := tx.Exec(`UPDATE msg SET status = MAX(status, ?) WHERE chat = ? AND id = ?`, r.status, r.chat, r.id); err != nil {
					return err
				}
			}
			return firstTime(tx, cs, r.id)
		}
	case !isNoRows(err):
		return err
	}
	var off, ln any
	if r.off >= 0 {
		off, ln = r.off, r.len
	}
	if _, err := tx.Exec(`INSERT INTO msg (chat, id, sender, sender_alt, from_me, t, kind, text, reply, album, secret,
			status, view_once, src, hash, seq, off, len, body, ord)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat, id) DO UPDATE SET sender = excluded.sender, sender_alt = excluded.sender_alt,
			from_me = excluded.from_me, t = excluded.t, kind = excluded.kind, text = excluded.text,
			reply = excluded.reply, album = excluded.album, secret = COALESCE(excluded.secret, msg.secret),
			status = MAX(msg.status, excluded.status), view_once = excluded.view_once, src = excluded.src,
			hash = excluded.hash, seq = excluded.seq, off = excluded.off, len = excluded.len, body = excluded.body,
			ord = MIN(msg.ord, excluded.ord)`,
		r.chat, r.id, r.sender, r.senderAlt, r.fromMe, r.t, r.kind, r.text, r.reply, r.album, r.secret,
		r.status, r.viewOnce, r.src, r.hash, r.seq, off, ln, r.body, r.ord); err != nil {
		return err
	}
	if err := firstTime(tx, cs, r.id); err != nil {
		return err
	}
	tx.Touch("message", r.key())
	tx.Touch("chat", r.chat)
	if len(r.secret) > 0 {
		return openSealed(tx, cs, r.id)
	}
	return nil
}

// firstTime keeps a message at the earliest time and arrival anything gave
// for it. a message the phone sent again after it did not decrypt comes
// stamped with the resend; the placeholder and the first copy carry when it
// was sent.
func firstTime(tx *core.Tx, cs chats, id string) error {
	var first, ord sql.NullInt64
	if err := tx.QueryRow(`SELECT MIN(t), MIN(ord) FROM (SELECT t, ord FROM msg_src WHERE id = ? AND `+cs.in("chat")+`
		UNION ALL SELECT t, ord FROM msg_wait WHERE id = ? AND `+cs.in("chat")+`)`,
		flat(id, cs, id, cs)...).Scan(&first, &ord); err != nil {
		return err
	}
	if !first.Valid {
		return nil
	}
	// every copy, as a copy under the chat's other address may have come first
	_, err := tx.Exec(`UPDATE msg SET t = MIN(t, ?), ord = MIN(ord, ?) WHERE id = ? AND `+cs.in("chat")+`
		AND (t > ? OR ord > ?)`, flat(first.Int64, ord.Int64, id, cs, first.Int64, ord.Int64)...)
	return err
}

// arrival orders messages that share a second, as whatsapp's own times are
// whole seconds: by when the input came, and inside a history conversation,
// which lists newest first, backwards. i is the place in that conversation,
// -1 for a message that is a whole input. it comes from the input alone, so
// a fold in any order agrees.
func arrival(at time.Time, i int) int64 {
	const places = 1 << 21
	return at.UnixMilli()<<21 | int64(places-2-min(i, places-2))
}

// sameChats is every address chat goes by for message id. a
// person has a pn and a lid chat, and a copy of one message, or something
// said about it, can sit under either before the two are known to be one
// person. a row under another address counts when its author is someone
// known to be this chat, by the row itself, another row of id or id_map. a
// message we sent counts everywhere: our ids don't repeat. a peer reusing an
// id from a chat it can't see stays in its own chat. known is more addresses
// known to be this chat, such as the sender of a copy being folded, or Me for
// a copy of ours.
func sameChats(tx *core.Tx, chat, id string, known ...string) (chats, error) {
	if !isPN(chat) && !isLID(chat) {
		return chats{chat}, nil
	}
	set := map[string]bool{chat: true}
	ours := false
	for _, k := range known {
		ours = ours || k == Me
		if isPN(k) || isLID(k) {
			set[k] = true
		}
	}
	// copy is 1 for a copy of the message, 0 for a row about it
	type ref struct {
		chat, by, alt string
		copy          bool
	}
	var refs []ref
	rows, err := tx.Query(`SELECT chat, sender, alt, 1 FROM msg_src WHERE id = ?1
		UNION ALL SELECT chat, CASE WHEN from_me THEN '`+Me+`' ELSE sender END, '', 1 FROM msg_wait WHERE id = ?1
		UNION ALL SELECT chat, by, by_alt, 0 FROM f_revoke WHERE target = ?1
		UNION ALL SELECT chat, by, by_alt, 0 FROM f_edit WHERE target = ?1
		UNION ALL SELECT chat, by, by_alt, 0 FROM f_pin WHERE target = ?1
		UNION ALL SELECT chat, by, by_alt, 0 FROM f_keep WHERE target = ?1
		UNION ALL SELECT chat, sender, '', 0 FROM f_reaction WHERE target = ?1
		UNION ALL SELECT chat, sender, '', 0 FROM f_enc WHERE target = ?1
		UNION ALL SELECT chat, '`+Me+`', '', 0 FROM msg_gone WHERE id = ?1
		UNION ALL SELECT chat, '`+Me+`', '', 0 FROM msg_local WHERE id = ?1`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.chat, &r.by, &r.alt, &r.copy); err != nil {
			rows.Close()
			return nil, err
		}
		if isPN(r.chat) || isLID(r.chat) {
			refs = append(refs, r)
			ours = ours || r.copy && r.by == Me
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// nearly always every row is under chat, and nothing else matters
	if !slices.ContainsFunc(refs, func(r ref) bool { return r.chat != chat }) {
		return chats{chat}, nil
	}
	if ours {
		for _, r := range refs {
			set[r.chat] = true
		}
		return chats(slices.Sorted(maps.Keys(set))), nil
	}
	mapped := map[string]bool{}
	for {
		for grew := true; grew; {
			grew = false
			for _, r := range refs {
				// what we did says nothing about whose chat it is
				if r.by == Me || !set[r.chat] && !set[r.by] && !set[r.alt] {
					continue
				}
				for _, a := range []string{r.chat, r.by, r.alt} {
					if a != "" && !set[a] && (isPN(a) || isLID(a)) {
						set[a], grew = true, true
					}
				}
			}
		}
		if !slices.ContainsFunc(refs, func(r ref) bool { return !set[r.chat] }) {
			break
		}
		// rows left out: ask id_map about the addresses not asked yet
		grew := false
		for _, a := range slices.Sorted(maps.Keys(set)) {
			if mapped[a] {
				continue
			}
			mapped[a] = true
			other, err := idMapPartners(tx, a)
			if err != nil {
				return nil, err
			}
			for _, o := range other {
				if !set[o] {
					set[o], grew = true, true
				}
			}
		}
		if !grew {
			break
		}
	}
	return chats(slices.Sorted(maps.Keys(set))), nil
}

// idMapPartners is the pn of lid a, or the lids of pn a.
func idMapPartners(tx *core.Tx, a string) ([]string, error) {
	rows, err := tx.Query(`SELECT pn FROM id_map WHERE lid = ?1 UNION SELECT lid FROM id_map WHERE pn = ?1`, a)
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

// chats is every address one chat goes by, as sameChats has it
type chats []string

// in is the sql for "column col is one of cs", a ? for each: bind cs
// through flat
func (cs chats) in(col string) string {
	return col + ` IN (` + strings.TrimSuffix(strings.Repeat(`?, `, len(cs)), `, `) + `)`
}

// flat is args with each chats in it spread out, one arg an address
func flat(args ...any) []any {
	out := make([]any, 0, len(args)+1)
	for _, a := range args {
		if cs, ok := a.(chats); ok {
			for _, c := range cs {
				out = append(out, c)
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func exists(tx *core.Tx, q string, args ...any) (bool, error) {
	var one int
	err := tx.QueryRow(q, args...).Scan(&one)
	if isNoRows(err) {
		return false, nil
	}
	return err == nil, err
}

// targetGone says a delete took the message a fact is about, or will take it
// the moment it shows up.
func targetGone(tx *core.Tx, chat, id string, known ...string) (bool, error) {
	cs, err := sameChats(tx, chat, id, known...)
	if err != nil {
		return false, err
	}
	if gone, err := exists(tx, `SELECT 1 FROM msg_gone WHERE id = ? AND `+cs.in("chat")+` LIMIT 1`, flat(id, cs)...); err != nil || gone {
		return gone, err
	}
	if gone, err := exists(tx, `SELECT 1 FROM f_revoke WHERE target = ? AND ok = 1 AND `+cs.in("chat")+` LIMIT 1`, flat(id, cs)...); err != nil || gone {
		return gone, err
	}
	return exists(tx, `SELECT 1 FROM appstate WHERE kind = ? AND op = 'set' AND b = ? AND `+cs.in("a")+` LIMIT 1`,
		flat(asDeleteForMe, id, cs)...)
}

// isGone says a delete for me or a chat clear already took this message.
// cs is what sameChats says for chat/id, clears is what clearChats read for
// chat, nil to read it here.
func isGone(tx *core.Tx, cs chats, chat, id string, t int64, clears *[]clear) (bool, error) {
	if gone, err := exists(tx, `SELECT 1 FROM appstate WHERE kind = ? AND op = 'set' AND b = ? AND `+cs.in("a")+` LIMIT 1`,
		flat(asDeleteForMe, id, cs)...); err != nil || gone {
		return gone, err
	}
	if clears == nil {
		cs, err := clearChats(tx, chat)
		if err != nil {
			return false, err
		}
		clears = &cs
	}
	for _, c := range *clears {
		if t > c.n*1000 {
			continue
		}
		if c.kind == asClearChat && c.b == "0" {
			starred, err := isStarred(tx, cs, id)
			if err != nil {
				return false, err
			}
			if starred {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

// clear is a chat clear or delete in force.
type clear struct {
	kind, b string
	n       int64
}

// clearChats is every clear and delete of chat and its aliases. a history
// conversation reads it once for all its messages: nothing it folds can
// change it.
func clearChats(tx *core.Tx, chat string) ([]clear, error) {
	var out []clear
	for _, c := range aliases(tx, chat) {
		rows, err := tx.Query(`SELECT kind, b, n FROM appstate WHERE kind IN (?, ?) AND op = 'set' AND a = ?`, asClearChat, asDeleteChat, c)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var x clear
			if err := rows.Scan(&x.kind, &x.b, &x.n); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func isStarred(tx *core.Tx, cs chats, id string) (bool, error) {
	var on bool
	err := tx.QueryRow(`SELECT on_ FROM appstate WHERE kind = ? AND b = ? AND op = 'set' AND `+cs.in("a")+` LIMIT 1`,
		flat(asStar, id, cs)...).Scan(&on)
	if isNoRows(err) {
		return false, nil
	}
	return on, err
}

// aliases is chat and every address id_map joins it to.
func aliases(tx *core.Tx, chat string) []string {
	out := []string{chat}
	q := ""
	switch {
	case isLID(chat):
		q = `SELECT pn FROM id_map WHERE lid = ?`
	case isPN(chat):
		q = `SELECT lid FROM id_map WHERE pn = ?`
	default:
		return out
	}
	rows, err := tx.Query(q, chat)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// scrubMessage is a delete for me: the row and what was said about it go, and
// every body it came in is blanked in the log.
func scrubMessage(tx *core.Tx, chat, id string) error {
	cs, err := sameChats(tx, chat, id)
	if err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT chat, seq, off, len FROM msg_src WHERE id = ? AND `+cs.in("chat"), flat(id, cs)...)
	if err != nil {
		return err
	}
	type src struct {
		chat     string
		seq      int64
		off, len int
	}
	var srcs []src
	for rows.Next() {
		var s src
		if err := rows.Scan(&s.chat, &s.seq, &s.off, &s.len); err != nil {
			rows.Close()
			return err
		}
		srcs = append(srcs, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	drop := map[string]bool{chat: true}
	for _, s := range srcs {
		if err := scrubBody(tx, s.seq, s.off, s.len); err != nil {
			return err
		}
		drop[s.chat] = true
	}
	waits, err := tx.Query(`SELECT chat FROM msg_wait WHERE id = ? AND `+cs.in("chat"), flat(id, cs)...)
	if err != nil {
		return err
	}
	for waits.Next() {
		var c string
		if waits.Scan(&c) == nil {
			drop[c] = true
		}
	}
	waits.Close()
	for c := range drop {
		if err := dropMessage(tx, c, id); err != nil {
			return err
		}
	}
	return nil
}

func dropMessage(tx *core.Tx, chat, id string) error {
	if _, err := tx.Exec(`DELETE FROM msg WHERE chat = ? AND id = ?`, chat, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO msg_gone (chat, id) VALUES (?, ?) ON CONFLICT DO NOTHING`, chat, id); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT seq FROM f_edit WHERE chat = ? AND target = ?`, chat, id)
	if err != nil {
		return err
	}
	var seqs []int64
	for rows.Next() {
		var s int64
		if rows.Scan(&s) == nil {
			seqs = append(seqs, s)
		}
	}
	rows.Close()
	for _, s := range seqs {
		if err := scrubBody(tx, s, -1, -1); err != nil {
			return err
		}
	}
	for _, t := range []string{"f_reaction", "f_edit", "f_enc", "f_pin", "f_keep", "f_revoke"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE chat = ? AND target = ?`, chat, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM msg_wait WHERE chat = ? AND id = ?`, chat, id); err != nil {
		return err
	}
	if err := dropLocal(tx, chat, id); err != nil {
		return err
	}
	if err := forgetLive(tx, chats{chat}, id); err != nil {
		return err
	}
	tx.Touch("message", chat+":"+id)
	tx.Touch("chat", chat)
	return nil
}

// scrubChat applies every clear and delete of chat and its aliases to the
// rows already here.
func scrubChat(tx *core.Tx, chat string) error {
	chats := aliases(tx, chat)
	for _, c := range chats {
		rows, err := tx.Query(`SELECT id, t FROM msg_src WHERE chat = ? UNION SELECT id, t FROM msg_wait WHERE chat = ?`, c, c)
		if err != nil {
			return err
		}
		type m struct {
			id string
			t  int64
		}
		var ms []m
		for rows.Next() {
			var x m
			if err := rows.Scan(&x.id, &x.t); err != nil {
				rows.Close()
				return err
			}
			ms = append(ms, x)
		}
		rows.Close()
		for _, x := range ms {
			cs, err := sameChats(tx, c, x.id)
			if err != nil {
				return err
			}
			gone, err := isGone(tx, cs, c, x.id, x.t, nil)
			if err != nil {
				return err
			}
			if gone {
				if err := scrubMessage(tx, c, x.id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// scrubBody blanks a body in the log. a history message keeps its key and
// time so a rebuild still knows what was deleted; the rest of its bytes
// become an unknown field of the same length, so the offsets of the messages
// after it hold. a live location keeps that it was one, see liveMarker.
func scrubBody(tx *core.Tx, seq int64, off, ln int) error {
	if off < 0 {
		var kind string
		var body []byte
		if err := tx.QueryRow(`SELECT kind, body FROM inputs WHERE seq = ?`, seq).Scan(&kind, &body); err != nil {
			if isNoRows(err) {
				return nil
			}
			return err
		}
		if kind == core.KindHistoryConversation || body == nil {
			return nil
		}
		var keep []byte
		var raw waE2E.Message
		if kind == core.KindMessage && proto.Unmarshal(body, &raw) == nil {
			if m := liveMarker(Unwrap(&raw).Msg); m != nil {
				keep, _ = marshal.Marshal(m)
			}
		}
		_, err := tx.Exec(`UPDATE inputs SET body = ? WHERE seq = ?`, keep, seq)
		return err
	}
	var body []byte
	if err := tx.QueryRow(`SELECT body FROM inputs WHERE seq = ?`, seq).Scan(&body); err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	if off+ln > len(body) {
		return nil
	}
	padded, ok := scrubbedHistoryMsg(body[off : off+ln])
	if !ok || bytes.Equal(padded, body[off:off+ln]) {
		return nil
	}
	copy(body[off:off+ln], padded)
	_, err := tx.Exec(`UPDATE inputs SET body = ? WHERE seq = ?`, body, seq)
	return err
}

// scrubbedHistoryMsg keeps a HistorySyncMsg's key and timestamp and pads the
// rest out to the same length.
func scrubbedHistoryMsg(b []byte) ([]byte, bool) {
	var hm waHistorySync.HistorySyncMsg
	if proto.Unmarshal(b, &hm) != nil {
		return nil, false
	}
	w := hm.GetMessage()
	keep := &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              w.GetKey(),
		MessageTimestamp: w.MessageTimestamp,
		Message:          liveMarker(Unwrap(w.GetMessage()).Msg),
	}}
	out, err := marshal.Marshal(keep)
	if err != nil || len(out) > len(b) {
		return nil, false
	}
	return pad(out, len(b))
}

// padField is a field number no HistorySyncMsg uses
const padField = 15

func pad(b []byte, n int) ([]byte, bool) {
	rest := n - len(b)
	if rest == 0 {
		return b, true
	}
	tag := protowire.AppendTag(nil, padField, protowire.BytesType)
	// the length prefix grows with the length, so try each size it can be
	for size := 1; size <= 5; size++ {
		l := rest - len(tag) - size
		if l < 0 {
			continue
		}
		if protowire.SizeVarint(uint64(l)) == size {
			out := protowire.AppendTag(b, padField, protowire.BytesType)
			out = protowire.AppendVarint(out, uint64(l))
			return append(out, make([]byte, l)...), true
		}
	}
	return nil, false
}

func foldUndecryptable(tx *core.Tx, in core.Input) error {
	h, err := head[core.UndecryptableHead](in)
	if err != nil {
		return err
	}
	chat := user(h.Chat)
	// hide is the server saying it was never meant for this device
	if chat == "" || h.ID == "" || h.FailMode == "hide" {
		return nil
	}
	sender := user(h.Sender)
	if h.FromMe {
		sender = Me
	}
	t := ms(h.T, in)
	cs, err := sameChats(tx, chat, h.ID, sender, h.SenderAlt)
	if err != nil {
		return err
	}
	if gone, err := isGone(tx, cs, chat, h.ID, t, nil); err != nil || gone {
		if err != nil {
			return err
		}
		return dropMessage(tx, chat, h.ID)
	}
	if _, err := tx.Exec(`INSERT INTO msg_wait (chat, id, sender, from_me, t, ord, unavailable) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat, id) DO UPDATE SET unavailable = MAX(msg_wait.unavailable, excluded.unavailable),
			t = MIN(msg_wait.t, excluded.t), ord = MIN(msg_wait.ord, excluded.ord)`,
		chat, h.ID, sender, h.FromMe, t, arrival(in.At, -1), h.UnavailableType); err != nil {
		return err
	}
	// the message itself may be here already, sent again by the phone
	rows, err := tx.Query(`SELECT chat FROM msg WHERE id = ? AND `+cs.in("chat"), flat(h.ID, cs)...)
	if err != nil {
		return err
	}
	var copies []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			copies = append(copies, c)
		}
	}
	rows.Close()
	if len(copies) > 0 {
		if err := firstTime(tx, cs, h.ID); err != nil {
			return err
		}
	}
	for _, c := range copies {
		tx.Touch("message", c+":"+h.ID)
	}
	tx.Touch("message", chat+":"+h.ID)
	tx.Touch("chat", chat)
	_, err = checkRevokes(tx, cs, h.ID)
	return err
}

func foldReceipt(tx *core.Tx, in core.Input) error {
	h, err := head[core.ReceiptHead](in)
	if err != nil {
		return err
	}
	chat := user(h.Chat)
	if chat == "" {
		return nil
	}
	who := user(h.Sender)
	if h.FromMe {
		who = Me
	}
	typ := h.Type
	if typ == "" {
		typ = "delivered"
	}
	switch typ {
	case "retry", "server-error", "hist_sync", "peer_msg", "inactive":
		return nil
	}
	t := ms(h.T, in)
	for _, id := range h.IDs {
		if _, err := tx.Exec(`INSERT INTO f_receipt (chat, id, who, type, t) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (id, chat, who, type) DO UPDATE SET t = MIN(f_receipt.t, excluded.t)`, chat, id, who, typ, t); err != nil {
			return err
		}
		tx.Touch("message", chat+":"+id)
	}
	tx.Touch("chat", chat)
	return nil
}

func putReaction(tx *core.Tx, f fact, target, emoji string) error {
	if gone, err := targetGone(tx, f.chat, target, f.known()...); err != nil || gone || target == "" {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO f_reaction (chat, target, sender, emoji, t) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (chat, target, sender) DO UPDATE SET emoji = excluded.emoji, t = excluded.t
		WHERE (excluded.t, excluded.emoji) > (f_reaction.t, f_reaction.emoji)`, f.chat, target, f.sender, emoji, f.t); err != nil {
		return err
	}
	tx.Touch("message", f.chat+":"+target)
	return nil
}

func putRevoke(tx *core.Tx, f fact, target string) error {
	if gone, err := targetGone(tx, f.chat, target, f.known()...); err != nil || gone || target == "" {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO f_revoke (chat, target, by, by_alt, t) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (chat, target, by) DO UPDATE SET by_alt = excluded.by_alt, t = excluded.t
		WHERE (excluded.t, excluded.by_alt) < (f_revoke.t, f_revoke.by_alt)`, f.chat, target, f.sender, f.senderAlt, f.t); err != nil {
		return err
	}
	tx.Touch("message", f.chat+":"+target)
	tx.Touch("chat", f.chat)
	cs, err := sameChats(tx, f.chat, target, f.known()...)
	if err != nil {
		return err
	}
	_, err = checkRevokes(tx, cs, target)
	return err
}

func putEdit(tx *core.Tx, f fact, target string, body []byte) error {
	if target == "" {
		return nil
	}
	if gone, err := targetGone(tx, f.chat, target, f.known()...); err != nil || gone {
		if err != nil || f.seq <= 0 {
			return err
		}
		return scrubBody(tx, f.seq, -1, -1)
	}
	sum := sha256.Sum256(body)
	if _, err := tx.Exec(`INSERT INTO f_edit (chat, target, by, by_alt, t, hash, body, seq) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat, target, by) DO UPDATE SET by_alt = excluded.by_alt, t = excluded.t, hash = excluded.hash,
			body = excluded.body, seq = excluded.seq
		WHERE (excluded.t, excluded.hash) > (f_edit.t, f_edit.hash)`, f.chat, target, f.sender, f.senderAlt, f.t, sum[:], body, f.seq); err != nil {
		return err
	}
	tx.Touch("message", f.chat+":"+target)
	tx.Touch("chat", f.chat)
	return nil
}

func putPin(tx *core.Tx, f fact, target string, pinned bool, secs uint32, phone bool) error {
	if gone, err := targetGone(tx, f.chat, target, f.known()...); err != nil || gone || target == "" {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO f_pin (chat, target, by, by_alt, pinned, t, secs, phone) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat, target, by) DO UPDATE SET by_alt = excluded.by_alt, pinned = excluded.pinned, t = excluded.t,
			secs = excluded.secs, phone = excluded.phone
		WHERE (excluded.t, excluded.pinned, excluded.secs, excluded.phone, excluded.by_alt)
			> (f_pin.t, f_pin.pinned, f_pin.secs, f_pin.phone, f_pin.by_alt)`,
		f.chat, target, f.sender, f.senderAlt, pinned, f.t, secs, phone); err != nil {
		return err
	}
	tx.Touch("message", f.chat+":"+target)
	tx.Touch("pins", f.chat)
	return nil
}

func putKeep(tx *core.Tx, f fact, target string, keep, phone bool) error {
	if gone, err := targetGone(tx, f.chat, target, f.known()...); err != nil || gone || target == "" {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO f_keep (chat, target, by, by_alt, keep, t, phone) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat, target, by) DO UPDATE SET by_alt = excluded.by_alt, keep = excluded.keep, t = excluded.t,
			phone = excluded.phone
		WHERE (excluded.t, excluded.keep, excluded.phone, excluded.by_alt) > (f_keep.t, f_keep.keep, f_keep.phone, f_keep.by_alt)`,
		f.chat, target, f.sender, f.senderAlt, keep, f.t, phone); err != nil {
		return err
	}
	tx.Touch("message", f.chat+":"+target)
	return nil
}

// foldHistoryConversation stores each message of a history conversation by
// where its bytes sit in the body, so a read decodes only the one it needs.
func foldHistoryConversation(tx *core.Tx, in core.Input) error {
	h, err := head[core.HistoryConversationHead](in)
	if err != nil {
		return err
	}
	chat := user(h.ID)
	if chat == "" {
		return nil
	}
	clears, err := clearChats(tx, chat)
	if err != nil {
		return err
	}
	cv := &conv{clears: clears, names: map[string]stampedName{}}
	b := in.Body
	pos, i := 0, 0
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b, pos = b[n:], pos+n
		if num != 2 || typ != protowire.BytesType {
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			b, pos = b[m:], pos+m
			continue
		}
		v, m := protowire.ConsumeBytes(b)
		if m < 0 {
			return protowire.ParseError(m)
		}
		start := pos + (m - len(v))
		if err := foldHistoryMsg(tx, in, chat, v, start, h.Offset+i, cv); err != nil {
			return err
		}
		i++
		b, pos = b[m:], pos+m
	}
	for sender, n := range cv.names {
		if err := setName(tx, sender, NamePush, n.name, n.t); err != nil {
			return err
		}
	}
	tx.Touch("chat", chat)
	return nil
}

func foldHistoryMsg(tx *core.Tx, in core.Input, chat string, b []byte, off, i int, cv *conv) error {
	var hm waHistorySync.HistorySyncMsg
	if err := proto.Unmarshal(b, &hm); err != nil {
		return err
	}
	w := hm.GetMessage()
	key := w.GetKey()
	if key.GetID() == "" {
		return nil
	}
	fromMe := key.GetFromMe()
	sender := Me
	if !fromMe {
		sender = user(key.GetParticipant())
		if sender == "" {
			sender = user(w.GetParticipant())
		}
		if sender == "" {
			sender = chat
		}
	}
	t := int64(w.GetMessageTimestamp()) * 1000
	if t == 0 {
		t = in.At.UnixMilli()
	}
	sum := sha256.Sum256(b)
	r := row{
		chat: chat, id: key.GetID(), sender: sender, fromMe: fromMe, t: t,
		status: histStatus(w), secret: w.GetMessageSecret(),
		src: srcHistory, hash: sum[:], seq: in.Seq, off: off, len: len(b), ord: arrival(in.At, i), conv: cv, body: b,
	}
	if n := w.GetPushName(); !fromMe && n != "" {
		if old, ok := cv.names[sender]; !ok || t > old.t || t == old.t && n > old.name {
			cv.names[sender] = stampedName{n, t}
		}
	}
	if err := foldHistoryFacts(tx, r, w); err != nil {
		return err
	}
	if stub := w.GetMessageStubType(); stub != waWeb.WebMessageInfo_UNKNOWN {
		r.kind = "stub:" + strconv.Itoa(int(stub))
		if w.GetMessage() == nil {
			return putMsg(tx, r)
		}
	}
	if w.GetMessage() == nil {
		// scrubbed, or nothing to show
		return putMsg(tx, r)
	}
	if r.kind != "" {
		// a stub with a message too: keep the stub
		return putMsg(tx, r)
	}
	return foldContent(tx, r, w.GetMessage(), fromMe)
}

// keySender is who sent the message a key names, as seen from chat.
func keySender(chat string, k *waCommon.MessageKey) string {
	if k.GetFromMe() {
		return Me
	}
	if p := user(k.GetParticipant()); p != "" {
		return p
	}
	return user(k.GetRemoteJID())
}

// foldHistoryFacts takes what history attaches to a message: reactions,
// votes, responses, receipts, pins.
func foldHistoryFacts(tx *core.Tx, r row, w *waWeb.WebMessageInfo) error {
	for _, x := range w.GetReactions() {
		f := fact{chat: r.chat, sender: keySender(r.chat, x.GetKey()), t: x.GetSenderTimestampMS(), seq: r.seq, ord: r.ord}
		if err := putReaction(tx, f, r.id, x.GetText()); err != nil {
			return err
		}
	}
	for _, x := range w.GetPollUpdates() {
		plain, err := marshal.Marshal(x.GetVote())
		if err != nil {
			return err
		}
		f := fact{chat: r.chat, sender: keySender(r.chat, x.GetPollUpdateMessageKey()), t: x.GetSenderTimestampMS(), seq: r.seq, ord: r.ord}
		if err := putEnc(tx, f, &waCommon.MessageKey{ID: &r.id}, useVote, nil, nil, plain); err != nil {
			return err
		}
	}
	for _, x := range w.GetEventResponses() {
		plain, err := marshal.Marshal(x.GetEventResponseMessage())
		if err != nil {
			return err
		}
		f := fact{chat: r.chat, sender: keySender(r.chat, x.GetEventResponseMessageKey()), t: x.GetTimestampMS(), seq: r.seq, ord: r.ord}
		if err := putEnc(tx, f, &waCommon.MessageKey{ID: &r.id}, useEvent, nil, nil, plain); err != nil {
			return err
		}
	}
	if p := w.GetPinInChat(); p != nil && p.GetKey().GetID() != "" {
		f := fact{chat: r.chat, sender: keySender(r.chat, p.GetKey()), t: p.GetSenderTimestampMS(), seq: r.seq, ord: r.ord}
		if err := putPin(tx, f, r.id, p.GetType() == waWeb.PinInChat_PIN_FOR_ALL, 0, true); err != nil {
			return err
		}
	}
	if k := w.GetKeepInChat(); k != nil {
		f := fact{chat: r.chat, t: k.GetClientTimestampMS(), seq: r.seq, ord: r.ord}
		if err := putKeep(tx, f, r.id, k.GetKeepType() == waE2E.KeepType_KEEP_FOR_ALL, true); err != nil {
			return err
		}
	}
	for _, x := range w.GetUserReceipt() {
		who := user(x.GetUserJID())
		if who == "" {
			continue
		}
		for typ, t := range map[string]int64{"delivered": x.GetReceiptTimestamp(), "read": x.GetReadTimestamp(), "played": x.GetPlayedTimestamp()} {
			if t <= 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO f_receipt (chat, id, who, type, t) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (id, chat, who, type) DO UPDATE SET t = MIN(f_receipt.t, excluded.t)`, r.chat, r.id, who, typ, t*1000); err != nil {
				return err
			}
		}
	}
	return nil
}

// histStatus is history's status for one of our messages, shifted by one so
// 0 stays "history said nothing": 1 error, 2 pending, 3 server ack,
// 4 delivered, 5 read, 6 played.
func histStatus(w *waWeb.WebMessageInfo) int {
	if w.Status == nil {
		return 0
	}
	return int(w.GetStatus()) + 1
}
