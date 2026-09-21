package protocol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"sync"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// MessageLister supplies the `messages` view its rows. *store.DB implements it.
// ListMessages backs the live-edge (`latest`) window and an anchored window's
// older frontier (newest messages before the anchor); ListMessagesAfter backs
// the newer frontier (oldest messages after the anchor); GetMessage fetches the
// anchor row itself. ListMessagesAround resolves/validates a message-id anchor;
// ListMessagesAroundUnread resolves the oldest-unread anchor id from the chat's
// unread count, which GetChat supplies.
type MessageLister interface {
	ListMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]store.Message, error)
	ListMessagesAfter(ctx context.Context, chatID string, limit int, afterMessageID string) ([]store.Message, error)
	ListMessagesAround(ctx context.Context, chatID string, limit int, targetMessageID string) ([]store.Message, error)
	ListMessagesAroundUnread(ctx context.Context, chatID string, limit int, unreadCount int) ([]store.Message, string, error)
	GetMessage(ctx context.Context, id string) (store.Message, error)
	GetChat(ctx context.Context, chatID string) (store.Chat, error)
}

// messagesUnboundedLimit caps an unwindowed subscription's fetch. A messages
// subscription with no `limit` is unusual (a client almost always windows a
// conversation), but the engine permits it; this bound keeps a pathological
// "no limit" from trying to LIMIT on the entire chat history at once.
const messagesUnboundedLimit = 1 << 20

// messagesView is the per-chat conversation view. With the default `latest`
// anchor it is a live-edge prefix window: the newest N messages, new messages
// arrive unsolicited, and `extend` (always `older`) reaches back into the local
// store. The `unread` and message-id anchors instead pin a mid-history anchor
// (the oldest unread message, or a named message a frontend is jumping to) and
// present a bounded window around it; that window is a DirectionalSession —
// `extend` grows a chosen frontier (`older` up-history, `newer` toward present)
// one side at a time, and out-of-window messages never intrude (Model A: to
// follow the live edge, subscribe `latest`). Fetching older history *from the
// phone* is the separate `chat.request_older` command.
type messagesView struct {
	daemon *app.Daemon
	lister MessageLister
}

type messagesParams struct {
	ChatID string `json:"chat_id"`
	Anchor string `json:"anchor"`
	Limit  *int   `json:"limit"`
}

func (v messagesView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p messagesParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed messages params")
		}
	}
	if p.ChatID == "" {
		return nil, nil, errorf(CodeInvalidParams, "messages params must carry a chat_id")
	}

	ctx, cancelCtx := context.WithCancel(context.Background())
	anchorID, meta, verr := v.resolveAnchor(ctx, p)
	if verr != nil {
		cancelCtx()
		return nil, nil, verr
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	newFeed := func() messagesChatFeed {
		return messagesChatFeed{
			lister:       v.lister,
			daemon:       v.daemon,
			chatID:       p.ChatID,
			eventsCancel: cancel,
			ctx:          ctx,
			cancelCtx:    cancelCtx,
			done:         make(chan struct{}),
			downloading:  activeDownloadsFor(v.daemon, p.ChatID),
		}
	}
	if anchorID == "" {
		s := &latestMessagesSession{messagesChatFeed: newFeed()}
		go s.run(events, invalidate)
		return s, meta, nil
	}
	older, newer := initialAnchorReach(p.Limit)
	s := &anchoredMessagesSession{
		messagesChatFeed: newFeed(),
		anchorID:         anchorID,
		olderReach:       older,
		newerReach:       newer,
	}
	go s.run(events, invalidate)
	return s, meta, nil
}

// initialAnchorReach splits the subscribe limit into a balanced older/newer
// reach around the anchor. The anchor itself takes one slot; the remainder is
// halved with any odd extra going to the newer side (newer-biased). No limit
// falls back to the unbounded cap; the real fetch is still bounded by how many
// messages exist.
func initialAnchorReach(limit *int) (older, newer int) {
	total := messagesUnboundedLimit
	if limit != nil && *limit > 0 {
		total = *limit
	}
	half := (total - 1) / 2
	return half, (total - 1) - half
}

// resolveAnchor turns the `anchor` param into a fixed anchor message id (empty
// for the live edge) and the subscribe meta. The anchor is pinned once here so
// the window stays put as messages come and go and the reported `anchor_id`
// never drifts. `unread` with nothing unread (or an unresolvable count)
// degrades to the live edge with no `anchor_id`, exactly as if `latest` were
// requested.
func (v messagesView) resolveAnchor(ctx context.Context, p messagesParams) (string, map[string]any, *Error) {
	if p.Anchor == "" || p.Anchor == "latest" {
		return "", nil, nil
	}
	// Every other anchor resolves against the store; a nil lister (a fixture or
	// misconfiguration) would otherwise nil-deref here. Fail cleanly instead —
	// the live-edge cases above never touch the lister and stay usable. F25.
	if v.lister == nil {
		return "", nil, errorf(CodeInternal, "messages view has no lister")
	}
	switch p.Anchor {
	case "unread":
		chat, err := v.lister.GetChat(ctx, p.ChatID)
		if err != nil {
			log.Printf("protocol: messages unread anchor: get chat %q: %v", p.ChatID, err)
			return "", nil, nil
		}
		if chat.UnreadCount <= 0 {
			return "", nil, nil
		}
		_, anchorID, err := v.lister.ListMessagesAroundUnread(ctx, p.ChatID, 1, int(chat.UnreadCount))
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("protocol: messages unread anchor: resolve %q: %v", p.ChatID, err)
			}
			return "", nil, nil
		}
		return anchorID, map[string]any{"anchor_id": anchorID}, nil
	default:
		// Any other value is a message-id anchor. Validate it belongs to this
		// chat by fetching the smallest possible window around it.
		window, err := v.lister.ListMessagesAround(ctx, p.ChatID, 1, p.Anchor)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, errorf(CodeNotFound, "no message %q in chat %q", p.Anchor, p.ChatID)
			}
			log.Printf("protocol: messages anchor %q: %v", p.Anchor, err)
			return "", nil, errorf(CodeInternal, "resolve message anchor")
		}
		// The anchor comes back from that window rather than from the request,
		// because the store redirects an id the transcript does not show to the
		// row that does show it: asking for a picture inside an album anchors
		// the album. `anchor_id` then says where the jump actually landed.
		anchorID := p.Anchor
		if len(window) > 0 && window[0].ID != "" {
			anchorID = window[0].ID
		}
		return anchorID, map[string]any{"anchor_id": anchorID}, nil
	}
}

// messagesChatFeed is the plumbing both session shapes share: it watches
// daemon events and invalidates the window whenever a message in this chat may
// have changed. Items always re-reads the store, so a spurious invalidate just
// recomputes to no diff.
type messagesChatFeed struct {
	lister       MessageLister
	daemon       *app.Daemon
	chatID       string
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once

	// downloadMu guards downloading: the ids in this chat with a media fetch in
	// flight right now. It lives on the message row rather than being joined
	// from `transfers` because the two are separate subscriptions recomputed on
	// separate goroutines: a frontend joining them saw the transfer disappear
	// before the path arrived and flashed its download button back. One row,
	// one update, no ordering to get wrong.
	downloadMu  sync.Mutex
	downloading map[string]bool

	// subjectsMu guards avatarSubjects: the ids whose avatar actually appears
	// in the window right now (every sender in it, plus the chat itself).
	// Avatar fetching is demand-driven and bursty, and an avatar for some
	// unrelated chat used to re-read and re-diff this whole window; recording
	// what the last Items() call actually returned makes the filter exact.
	subjectsMu     sync.Mutex
	avatarSubjects map[string]bool
}

// noteAvatarSubjects records the avatar subjects of the window just built.
func (f *messagesChatFeed) noteAvatarSubjects(msgs []store.Message) {
	subjects := make(map[string]bool, len(msgs)+1)
	subjects[f.chatID] = true
	for _, m := range msgs {
		if m.SenderID != "" {
			subjects[m.SenderID] = true
		}
	}
	f.subjectsMu.Lock()
	f.avatarSubjects = subjects
	f.subjectsMu.Unlock()
}

// activeDownloadsFor snapshots this chat's in-flight fetches from the daemon,
// the same source `transfers` seeds itself from.
func activeDownloadsFor(daemon *app.Daemon, chatID string) map[string]bool {
	fresh := make(map[string]bool)
	if daemon == nil {
		return fresh
	}
	for _, download := range daemon.ActiveMediaDownloads() {
		if download.MessageID != "" && download.ChatID == chatID && download.Downloading {
			fresh[download.MessageID] = true
		}
	}
	return fresh
}

// reloadDownloading rebuilds the in-flight set from the daemon snapshot after a
// dropped-event gap: it is daemon memory rather than store state, so a resync
// has no other way to recover it.
func (f *messagesChatFeed) reloadDownloading() {
	fresh := activeDownloadsFor(f.daemon, f.chatID)
	f.downloadMu.Lock()
	f.downloading = fresh
	f.downloadMu.Unlock()
}

// noteDownloadChanged folds one transfer event in and reports whether the
// window has to be recomputed. Progress ticks arrive several times a second per
// transfer and do not move this bool, so only the two edges cost a re-read of
// the window.
func (f *messagesChatFeed) noteDownloadChanged(evt app.MediaDownloadEvent) bool {
	f.downloadMu.Lock()
	defer f.downloadMu.Unlock()
	if f.downloading == nil {
		f.downloading = make(map[string]bool)
	}
	was := f.downloading[evt.MessageID]
	if was == evt.Downloading {
		return false
	}
	if evt.Downloading {
		f.downloading[evt.MessageID] = true
	} else {
		delete(f.downloading, evt.MessageID)
	}
	return true
}

func (f *messagesChatFeed) isDownloading(id string) bool {
	f.downloadMu.Lock()
	defer f.downloadMu.Unlock()
	return f.downloading[id]
}

func (f *messagesChatFeed) avatarInWindow(id string) bool {
	f.subjectsMu.Lock()
	defer f.subjectsMu.Unlock()
	return f.avatarSubjects[id]
}

func (f *messagesChatFeed) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-f.done:
			return
		case evt := <-events:
			if f.eventAffectsChat(evt) {
				invalidate()
			}
		}
	}
}

func (f *messagesChatFeed) eventAffectsChat(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync:
		// Dropped-event gap: re-read the store (Items is authoritative) and the
		// transfer set, which is daemon memory rather than store state and so
		// has no other way back.
		f.reloadDownloading()
		return true
	case app.DaemonEventMediaDownloadChanged:
		if evt.MediaDownload.ChatID != f.chatID || evt.MediaDownload.MessageID == "" {
			return false
		}
		return f.noteDownloadChanged(evt.MediaDownload)
	case app.DaemonEventNewMessage, app.DaemonEventMessageUpdated:
		return evt.Message.ChatID == f.chatID
	case app.DaemonEventMessageDeleted, app.DaemonEventChatDeleted:
		return evt.DeletedChatID == f.chatID
	case app.DaemonEventChatCleared:
		return evt.Chat.ID == f.chatID
	case app.DaemonEventHistoryBackfilled:
		return evt.HistorySync.ChatID == f.chatID
	case app.DaemonEventAvatarUpdated:
		// Only re-read when the avatar belongs to something the window renders.
		// Matched on id alone, not (kind, id): a message row resolves its
		// sender's avatar from either the sender or the chat subject.
		return f.avatarInWindow(evt.Avatar.ID)
	default:
		return false
	}
}

func (f *messagesChatFeed) Close() {
	f.closeOnce.Do(func() {
		f.cancelCtx()
		close(f.done)
		f.eventsCancel()
	})
}

// latestMessagesSession is the live-edge prefix window: Items returns the newest
// max messages slice-ordered newest-first (the engine keeps the prefix = the
// newest), while each item carries an ascending timestamp sort key so the client
// renders oldest→newest. `extend` (always `older`) grows the prefix.
type latestMessagesSession struct {
	messagesChatFeed
}

func (s *latestMessagesSession) Items(max int) []Item {
	items, _ := s.ItemsErr(max)
	return items
}

func (s *latestMessagesSession) ItemsErr(max int) ([]Item, error) {
	if s.lister == nil {
		return nil, nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	msgs, err := s.lister.ListMessages(s.ctx, s.chatID, limit, "")
	if err != nil {
		log.Printf("protocol: list latest messages for view: %v", err)
		return nil, err
	}
	reverseMessages(msgs) // newest-first for the prefix window
	s.noteAvatarSubjects(msgs)
	items := make([]Item, 0, len(msgs))
	for _, m := range msgs {
		items = append(items, messageWireItem(m, s.isDownloading))
	}
	return items, nil
}

// anchoredMessagesSession is a DirectionalSession: a bounded window pinned
// around a fixed anchor whose two frontiers grow independently. It owns its
// window — olderReach messages before the anchor, the anchor, newerReach after
// — so `extend older`/`extend newer` bump one frontier at a time. Items returns
// the whole current window (the engine does no prefix trim); Exhausted reports
// the frontier last extended so `ready` answers the direction the client grew.
type anchoredMessagesSession struct {
	messagesChatFeed
	anchorID string

	mu             sync.Mutex
	olderReach     int
	newerReach     int
	lastDir        string // "", "older", "newer"
	olderExhausted bool
	newerExhausted bool
	// atLiveEdge latches once the newer frontier has reached the newest message
	// in the chat. From then on the window stays adjacent to the live edge
	// (PROTOCOL.md, "Windows"): messages arriving at the present are contiguous
	// with it, so they belong in it and are delivered as ordinary upserts. Before
	// the latch, the newer side is trimmed to newerReach: a message past the
	// frontier is separated from the window by a gap the frontend cannot render.
	atLiveEdge bool
}

func (s *anchoredMessagesSession) ExtendWindow(direction string, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if direction == "newer" {
		s.newerReach += count
		s.lastDir = "newer"
	} else {
		s.olderReach += count
		s.lastDir = "older"
	}
}

func (s *anchoredMessagesSession) Exhausted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.lastDir {
	case "older":
		return s.olderExhausted
	case "newer":
		return s.newerExhausted
	default: // before any extend: exhausted only if the whole chat fits
		return s.olderExhausted && s.newerExhausted
	}
}

func (s *anchoredMessagesSession) Items(max int) []Item {
	items, _ := s.ItemsErr(max)
	return items
}

func (s *anchoredMessagesSession) ItemsErr(int) ([]Item, error) {
	if s.lister == nil {
		return nil, nil
	}
	s.mu.Lock()
	olderN, newerN, live := s.olderReach, s.newerReach, s.atLiveEdge
	s.mu.Unlock()
	ctx := s.ctx

	anchor, err := s.lister.GetMessage(ctx, s.anchorID)
	if err != nil {
		// The anchor was validated at subscribe; if it is later deleted the
		// window collapses to empty rather than erroring the live view. Any
		// other failure is a read that did not happen, not an empty window.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		log.Printf("protocol: anchored messages get anchor %q: %v", s.anchorID, err)
		return nil, err
	}

	// Older frontier: the olderN messages nearest the anchor on the older side
	// (ascending). Fetch one extra to detect exhaustion, then keep the newest.
	older, err := s.lister.ListMessages(ctx, s.chatID, olderN+1, s.anchorID)
	if err != nil {
		log.Printf("protocol: anchored messages older frontier: %v", err)
		return nil, err
	}
	olderExhausted := len(older) <= olderN
	if len(older) > olderN {
		older = older[len(older)-olderN:]
	}

	// Newer frontier: the newerN messages nearest the anchor on the newer side
	// (ascending). Same one-extra exhaustion probe, keep the oldest. Once the
	// frontier has reached the live edge the trim stops for good, because
	// everything after the anchor is contiguous with the window, including what
	// arrives
	// while it is open, so the fetch is unbounded and the frontier stays
	// exhausted rather than re-opening a gap behind each new message.
	newerLimit := newerN + 1
	if live {
		newerLimit = messagesUnboundedLimit
	}
	newer, err := s.lister.ListMessagesAfter(ctx, s.chatID, newerLimit, s.anchorID)
	if err != nil {
		log.Printf("protocol: anchored messages newer frontier: %v", err)
		return nil, err
	}
	newerExhausted := live || len(newer) <= newerN
	if !live && len(newer) > newerN {
		newer = newer[:newerN]
	}

	s.mu.Lock()
	s.olderExhausted = olderExhausted
	s.newerExhausted = newerExhausted
	if newerExhausted {
		s.atLiveEdge = true
		if len(newer) > s.newerReach {
			// Keep the reach describing the window actually held, so an extend
			// after the latch cannot ask for less than is already delivered.
			s.newerReach = len(newer)
		}
	}
	s.mu.Unlock()

	window := make([]store.Message, 0, len(older)+1+len(newer))
	window = append(window, older...)
	window = append(window, anchor)
	window = append(window, newer...)
	s.noteAvatarSubjects(window)

	items := make([]Item, 0, len(window))
	for _, m := range window {
		items = append(items, messageWireItem(m, s.isDownloading))
	}
	return items, nil
}

// messageWireItem projects a stored message into a view Item: stable id, the
// ascending timestamp sort key (render order), and the wire shape. `downloading`
// is the one field that is not store state: it is live daemon memory, folded in
// here so a row's fetch state and its path can never disagree.
//
// It is asked per id rather than passed as one bool because an album's tiles
// are messages too, each with a fetch of its own: one tile's progress ring must
// not be another's.
func messageWireItem(m store.Message, downloading func(id string) bool) Item {
	item := messageItemFromStore(m)
	if item.Media != nil && downloading(m.ID) {
		item.Media.Downloading = true
	}
	if item.Album != nil {
		for i := range item.Album.Items {
			tile := &item.Album.Items[i]
			if tile.Media != nil && downloading(tile.ID) {
				tile.Media.Downloading = true
			}
		}
	}
	return Item{ID: m.ID, Sort: messageSort(m), Data: item}
}

// reverseMessages flips a slice in place; the store returns ascending pages
// that the live-edge window renders newest-first.
func reverseMessages(msgs []store.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

// messageSort is the opaque ordering key: the stored millisecond sort key,
// zero-padded so the strings compare numerically, then the message id as a
// tiebreak. Ascending bytewise order renders oldest-first.
//
// The tiebreak is the id rather than local insert order on purpose. Insert
// order made a backfilled message sort after a live one from the same second,
// which is exactly backwards, and gave two devices holding the same
// conversation two different orders. The id is the same everywhere and does not
// change when a row is written again.
func messageSort(m store.Message) string {
	sortMS := m.SortMS
	if sortMS < 0 {
		sortMS = 0
	}
	return fmt.Sprintf("%020d-%s", sortMS, m.ID)
}

// messageItem is the wire shape of a conversation row. Every item carries a
// `kind` and a human-readable `fallback` (rule 5); media crosses only as file
// paths (rule 4).
type messageItem struct {
	ID          string            `json:"id"`
	ChatID      string            `json:"chat_id"`
	Kind        string            `json:"kind"`
	Fallback    string            `json:"fallback"`
	Text        string            `json:"text,omitempty"`
	Sender      messageSender     `json:"sender"`
	Timestamp   int64             `json:"timestamp"`
	Direction   string            `json:"direction"`
	Status      string            `json:"status,omitempty"`
	ReplyTo     *messageReply     `json:"reply_to,omitempty"`
	Reactions   []messageReaction `json:"reactions,omitempty"`
	Mentions    []messageMention  `json:"mentions,omitempty"`
	Edited      bool              `json:"edited,omitempty"`
	Revoked     bool              `json:"revoked,omitempty"`
	Starred     bool              `json:"starred,omitempty"`
	Forwarded   bool              `json:"forwarded,omitempty"`
	PinnedUntil int64             `json:"pinned_until,omitempty"`
	// ViewOnce marks our own view-once sends. Inbound view-once media is
	// never stored as media (phone-only tombstone), so this only appears on
	// outgoing rows.
	ViewOnce bool `json:"view_once,omitempty"`
	// Kept marks a disappearing message somebody asked to keep in the chat.
	Kept  bool          `json:"kept,omitempty"`
	Media *messageMedia `json:"media,omitempty"`

	// Kind-specific payloads. Exactly one is ever set, chosen by `kind`, and a
	// frontend that does not know the kind ignores all of them and renders
	// `fallback` instead (rule 5). Each lands with the phase that implements
	// its kind; the shapes themselves live in store/payloads.go, because the
	// store writes them and the protocol reads them back.
	Location *store.LocationPayload `json:"location,omitempty"`
	// Live is the state of a running live-location share, on its opening
	// message. It carries only what a bubble needs to describe itself; the
	// moving positions themselves are the `live_locations` view's job. It
	// rides the row's own payload rather than a join, so listing a thousand
	// messages costs no extra queries.
	Live *store.LiveSharePayload `json:"live,omitempty"`
	// Contacts holds one or more shared contact cards, parsed out of their
	// vCards so a frontend never has to.
	Contacts *store.ContactsPayload `json:"contacts,omitempty"`
	// Poll carries the question and its live tally. The tally is joined from
	// real tables at read time rather than snapshotted, so it is never stale.
	Poll *messagePoll `json:"poll,omitempty"`
	// Invite is an offer to join a group, carrying both the sender's own
	// snapshot of it and whatever the daemon resolved from the invite code,
	// including whether we are already a member.
	Invite *store.GroupInvitePayload `json:"invite,omitempty"`
	// Event is a scheduled event with where its RSVPs currently stand. The
	// answers are joined from a real table at read time rather than
	// snapshotted, so they are never stale.
	Event *messageEvent `json:"event,omitempty"`
	// Album is several pictures sent as one thing. Its items are whole message
	// items, so a tile keeps its own id, its own media object and its own
	// download state, and a frontend renders a tile with the code it already
	// has for a photo. The grouping is the daemon's: a frontend never sees the
	// children as separate rows and never has to merge them (rule 3).
	Album *messageAlbum `json:"album,omitempty"`
	// LinkPreview is the card the sender's client built for a link in the text.
	// It is the one payload that arrives on a row of another kind: `kind` stays
	// `text`, because the text is still the message and the preview only
	// describes what it points at.
	LinkPreview *store.LinkPreviewPayload `json:"link_preview,omitempty"`
	// Interactive is a business message: a header, some words and a set of
	// things the reader is invited to do. WhatsApp has four wire shapes for
	// that one idea and the daemon flattens all four into this, so a frontend
	// draws one card rather than four.
	Interactive *store.InteractivePayload `json:"interactive,omitempty"`
	// Commerce is a product, an order or a payment. The money crosses as the
	// integer WhatsApp sent (thousandths of a unit) plus its ISO 4217 code,
	// never as a formatted string: how a sum reads is a question about the
	// person reading it, and the daemon does not know them.
	Commerce *store.CommercePayload `json:"commerce,omitempty"`
	// StickerPack is a shared pack, with what the local library currently knows
	// about it. Whether it can be added, and whether it already has been, are
	// joined at read time, so a card never offers a pack that is already in the
	// picker.
	StickerPack *messageStickerPack `json:"sticker_pack,omitempty"`
	// CallLog is a call that happened. It is not a message anybody wrote, which
	// is why a frontend draws it centered rather than in somebody's bubble.
	CallLog *store.CallLogPayload `json:"call_log,omitempty"`
	// System is something the chat did to itself: a membership change, a
	// setting, a security code. Like a call log it is drawn centered, and for
	// the same reason: it has no author to put it beside.
	System *messageSystem `json:"system,omitempty"`
	// Waiting is a message that arrived but would not decrypt, and has been
	// asked for again. The row turns into the real message, in place and under
	// the same id, if the resend arrives.
	Waiting *store.WaitingPayload `json:"waiting,omitempty"`
}

// messageSystem is one thing that happened to a chat.
//
// It carries both a finished sentence and the parts it was built from. `text`
// is what rule 5 needs: any frontend can print it and be correct. The parts are
// what a frontend that speaks a language other than this daemon's needs, since
// the daemon composes in English and has no idea who is reading. Whether an
// event reads "Ana joined" or "You added Ana" is still decided here, in the
// flags; a frontend chooses words, never meaning.
type messageSystem struct {
	// Type names what happened ("group_join", "identity_change", …).
	Type string `json:"type"`
	Text string `json:"text"`
	// Actor is who did it, absent when the server reported no author (a join
	// through an invite link has nobody to credit).
	Actor *store.SystemParticipant `json:"actor,omitempty"`
	// Names are the first few people the event named. The list is bounded here
	// so a forty-person add crosses as three names and a number, which is what
	// the sentence says anyway.
	Names []store.SystemParticipant `json:"names,omitempty"`
	// Overflow is how many more people the event named beyond Names.
	Overflow int `json:"overflow,omitempty"`
	// Value is the new subject or description, for the kinds that set one.
	Value string `json:"value,omitempty"`
	// On is the direction of a two-state change: a timer turned on, a group
	// locked, messages restricted to admins.
	On bool `json:"on,omitempty"`
	// Seconds is the new disappearing-message timer, meaningful when On.
	Seconds uint32 `json:"seconds,omitempty"`
	// AboutSelf marks an event that named you: you were added, removed,
	// promoted, demoted, or your security code changed. It is what decides
	// whether the row reordered your chat list, with one exception: a changed
	// security code names you but stays quiet, because it fires whenever the
	// other side reinstalls and is nobody's doing.
	AboutSelf bool `json:"about_self,omitempty"`
}

// messageStickerPack is a shared sticker pack plus the library's answer about
// it, which is the half that keeps moving.
type messageStickerPack struct {
	PackID      string `json:"pack_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Description string `json:"description,omitempty"`
	Caption     string `json:"caption,omitempty"`
	Count       int    `json:"count,omitempty"`
	// Installable says the daemon can find this pack and `sticker_pack.install`
	// would work on it. False for a pack somebody assembled themselves, which
	// has no entry to install.
	Installable bool `json:"installable"`
	Installed   bool `json:"installed"`
}

// messageAlbum is a group of pictures sent together.
type messageAlbum struct {
	// Expected is how many pictures the sender said were coming. It can exceed
	// len(items) while an album is still arriving, or forever if one never did,
	// and that difference is worth showing rather than hiding.
	Expected int           `json:"expected,omitempty"`
	Items    []messageItem `json:"items"`
}

// messageEvent is a scheduled event and who has said they are coming.
type messageEvent struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	StartsAt    int64  `json:"starts_at,omitempty"`
	EndsAt      int64  `json:"ends_at,omitempty"`
	Canceled    bool   `json:"canceled,omitempty"`
	JoinLink    string `json:"join_link,omitempty"`
	// Location is the venue, in the same shape a shared place uses, so a
	// frontend draws an event's map with the code it already has. The map
	// itself arrives through the row's `media` object like any other.
	Location           *store.LocationPayload `json:"location,omitempty"`
	ExtraGuestsAllowed bool                   `json:"extra_guests_allowed,omitempty"`
	ScheduleCall       bool                   `json:"schedule_call,omitempty"`
	ReminderOffsetSecs int64                  `json:"reminder_offset_secs,omitempty"`

	Responders []messageEventResponder `json:"responders"`
	// GoingCount counts heads rather than answers: somebody bringing two guests
	// is three people at the door, and that is the number worth showing.
	GoingCount int `json:"going_count,omitempty"`
	// SelfResponse is our own answer ("going", "not_going", "maybe"), empty
	// when we have not answered, so a bubble knows which chip is lit without
	// searching the list for itself.
	SelfResponse string `json:"self_response,omitempty"`
	SelfGuests   int    `json:"self_guests,omitempty"`
}

type messageEventResponder struct {
	JID         string `json:"jid"`
	Name        string `json:"name,omitempty"`
	AvatarPath  string `json:"avatar_path,omitempty"`
	Response    string `json:"response"`
	ExtraGuests int    `json:"extra_guests,omitempty"`
	Timestamp   int64  `json:"timestamp,omitempty"`
	FromMe      bool   `json:"from_me,omitempty"`
}

// messagePoll is a poll and where its votes currently stand.
type messagePoll struct {
	Question        string              `json:"question,omitempty"`
	SelectableCount int                 `json:"selectable_count,omitempty"`
	AllowAddOption  bool                `json:"allow_add_option,omitempty"`
	EndsAt          int64               `json:"ends_at,omitempty"`
	Quiz            bool                `json:"quiz,omitempty"`
	Options         []messagePollOption `json:"options"`
	// TotalVoters counts people, not selections: a poll that allows several
	// answers would otherwise claim more votes than it has voters.
	TotalVoters int `json:"total_voters,omitempty"`
	// SelfVoted says whether we are among them, which is what tells a bubble
	// to show its result state rather than its blank one.
	SelfVoted bool `json:"self_voted,omitempty"`
}

type messagePollOption struct {
	// Index addresses the option in `poll.vote`; it is the option's position,
	// not its text, because two options may read the same.
	Index  int                `json:"index"`
	Name   string             `json:"name"`
	Voters []messagePollVoter `json:"voters,omitempty"`
	// SelfVoted marks our own choice, so a bubble does not have to search the
	// voter list for itself.
	SelfVoted bool `json:"self_voted,omitempty"`
}

type messagePollVoter struct {
	JID        string `json:"jid"`
	Name       string `json:"name,omitempty"`
	AvatarPath string `json:"avatar_path,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
	FromMe     bool   `json:"from_me,omitempty"`
}

type messageSender struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	AvatarPath string `json:"avatar_path,omitempty"`
	// Device is the sender's device id: 0 is the primary phone app,
	// anything else a linked device.
	Device uint16 `json:"device,omitempty"`
}

type messageReply struct {
	MessageID  string `json:"message_id"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	Text       string `json:"text,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Direction  string `json:"direction,omitempty"`
}

type messageReaction struct {
	Emoji      string `json:"emoji"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
	FromMe     bool   `json:"from_me,omitempty"`
}

type messageMention struct {
	JID  string `json:"jid"`
	Name string `json:"name,omitempty"`
}

type messageMedia struct {
	Mime          string `json:"mime,omitempty"`
	Width         int32  `json:"width,omitempty"`
	Height        int32  `json:"height,omitempty"`
	Animated      bool   `json:"animated,omitempty"`
	ThumbnailPath string `json:"thumbnail_path,omitempty"`
	Path          string `json:"path,omitempty"`
	DownloadError string `json:"download_error,omitempty"`
	// Downloading is true while a fetch for this message is in flight. Only the
	// `messages` view sets it; `transfers` still carries the byte counters.
	Downloading  bool   `json:"downloading,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	DurationSecs int32  `json:"duration_secs,omitempty"`
	Filename     string `json:"filename,omitempty"`
	PageCount    int32  `json:"page_count,omitempty"`
	// Waveform is the voice-note amplitude envelope: 64 buckets of 0-100. It
	// is the one piece of media data that crosses the socket rather than
	// living in a file, because it is tiny and the bubble needs it before any
	// download happens.
	Waveform []int `json:"waveform,omitempty"`
	Played   bool  `json:"played,omitempty"`
}

func messageItemFromStore(m store.Message) messageItem {
	kind := messageKind(m)
	item := messageItem{
		ID:          m.ID,
		ChatID:      m.ChatID,
		Kind:        kind,
		Fallback:    messageFallback(m),
		Kept:        m.IsKept,
		Text:        m.Text,
		Sender:      messageSender{ID: m.SenderID, Name: m.SenderName, AvatarPath: m.SenderAvatarLocalPath, Device: m.SenderDevice},
		Timestamp:   m.TimestampUnix,
		Direction:   m.Direction,
		Status:      m.Status,
		Edited:      m.IsEdited,
		Revoked:     m.IsRevoked,
		Starred:     m.IsStarred,
		Forwarded:   m.IsForwarded,
		PinnedUntil: m.PinnedUntil,
		ViewOnce:    m.IsViewOnce,
		Reactions:   messageReactions(m.Reactions),
		Mentions:    messageMentions(m.Mentions),
		Media:       messageMediaFromStore(m),
	}
	attachMessagePayload(&item, m)
	if r := m.ReplyTo; r.MessageID != "" {
		item.ReplyTo = &messageReply{
			MessageID:  r.MessageID,
			SenderID:   r.SenderID,
			SenderName: r.SenderName,
			Text:       r.Text,
			Kind:       mediaKindToWire(r.MediaKind),
			Direction:  r.Direction,
		}
	}
	return item
}

// messageKind maps the stored media kind to the wire `kind`. A revoked message
// carries no content, so it renders as an (empty) text bubble plus revoked:true.
func messageKind(m store.Message) string {
	if m.IsRevoked {
		return "text"
	}
	// Inbound view-once rows keep their real kind and keys in the store (so
	// an explicit `media.save` can fetch them), but they always render as the
	// `unsupported` tombstone: no bubble and no auto-download policy may
	// silently defeat the sender's view-once intent.
	if m.IsViewOnce && m.Direction == store.DirectionIncoming {
		return store.MediaKindUnsupported
	}
	return mediaKindToWire(m.MediaKind)
}

// mediaKindToWire maps a stored kind to the wire `kind`. The store constants
// are deliberately spelled the same as the protocol kinds (`image`, `video`,
// `voice`, `document`, ...), so this is a passthrough with one special case: a
// row carrying no media is a text message.
func mediaKindToWire(mediaKind string) string {
	if mediaKind == "" {
		return "text"
	}
	return mediaKind
}

// messageFallback is the one-line human rendering a frontend shows for any kind
// it does not implement (and the natural preview for the ones it does). It is
// the same rendering the chat row's preview uses, from the same table.
func messageFallback(m store.Message) string {
	return store.MessagePreviewLine(m)
}

// attachMessagePayload hangs the row's kind-specific object off the item. It is
// one decode of payload_json per row, and the switch keeps a kind from ever
// emitting a payload belonging to a different one.
func attachMessagePayload(item *messageItem, m store.Message) {
	if m.PayloadJSON == "" {
		return
	}
	payload := store.DecodePayload(m.PayloadJSON)
	switch m.MediaKind {
	case "":
		// A row with no media kind is a text message, and the only payload one
		// carries is the card for a link inside it.
		item.LinkPreview = payload.LinkPreview
	case store.MediaKindLocation:
		item.Location = payload.Location
	case store.MediaKindLiveLocation:
		item.Location = payload.Location
		item.Live = payload.LiveShare
	case store.MediaKindContact, store.MediaKindContacts:
		item.Contacts = payload.Contacts
	case store.MediaKindPoll:
		item.Poll = messagePollFromStore(m, payload.Poll)
	case store.MediaKindGroupInvite:
		item.Invite = payload.GroupInvite
	case store.MediaKindEvent:
		item.Event = messageEventFromStore(m, payload.Event)
	case store.MediaKindAlbum:
		item.Album = messageAlbumFromStore(m, payload.Album)
	case store.MediaKindInteractive:
		item.Interactive = payload.Interactive
	case store.MediaKindProduct, store.MediaKindOrder, store.MediaKindPayment:
		item.Commerce = payload.Commerce
	case store.MediaKindStickerPack:
		item.StickerPack = messageStickerPackFromStore(m, payload.StickerPack)
	case store.MediaKindCallLog:
		item.CallLog = payload.CallLog
	case store.MediaKindSystem:
		item.System = messageSystemFromStore(m, payload.System)
	case store.MediaKindWaiting:
		item.Waiting = payload.Waiting
	}
}

// messageSystemFromStore trims the stored event to what a pill needs. The full
// participant list stays in the store: a forty-person add crosses as three
// names and a number, because that is what the sentence already says.
func messageSystemFromStore(m store.Message, payload *store.SystemPayload) *messageSystem {
	if payload == nil {
		return nil
	}
	system := &messageSystem{
		Type:      payload.Type,
		Text:      m.PayloadSummary,
		Actor:     payload.Actor,
		Value:     payload.Value,
		On:        payload.On,
		Seconds:   payload.Seconds,
		AboutSelf: payload.AboutSelf,
	}
	for _, participant := range payload.Participants {
		if len(system.Names) >= systemWireNameLimit {
			system.Overflow++
			continue
		}
		system.Names = append(system.Names, participant)
	}
	return system
}

// systemWireNameLimit is how many names cross the wire. It matches the number
// the daemon's own sentence names before it starts counting, so `names` and
// `text` never disagree about who was listed.
const systemWireNameLimit = 3

// messageStickerPackFromStore joins what the share said with what the library
// says about it now.
func messageStickerPackFromStore(m store.Message, payload *store.StickerPackPayload) *messageStickerPack {
	if payload == nil {
		return nil
	}
	pack := &messageStickerPack{
		PackID:      payload.PackID,
		Name:        payload.Name,
		Publisher:   payload.Publisher,
		Description: payload.Description,
		Caption:     payload.Caption,
		Count:       payload.Count,
	}
	if state := m.StickerPack; state != nil {
		pack.Installable = state.Known
		pack.Installed = state.Installed
	}
	return pack
}

// messageAlbumFromStore projects an album's children into the wire item that
// carries them. Each child goes through the same builder the transcript uses,
// so a tile is a message item in every respect and nothing about a photo has to
// be reimplemented for the version of it that lives inside an album.
func messageAlbumFromStore(m store.Message, payload *store.AlbumPayload) *messageAlbum {
	album := &messageAlbum{Expected: payload.Expected(), Items: []messageItem{}}
	for _, child := range m.Album {
		album.Items = append(album.Items, messageItemFromStore(child))
	}
	// An album whose children all arrived describes itself by what it holds. The
	// promise only stays interesting while it is unkept.
	if album.Expected <= len(album.Items) {
		album.Expected = 0
	}
	return album
}

// messagePollFromStore joins a poll's fixed settings with the tally the store
// attached to the row.
func messagePollFromStore(m store.Message, payload *store.PollPayload) *messagePoll {
	if payload == nil {
		return nil
	}
	poll := &messagePoll{
		Question:        payload.Question,
		SelectableCount: payload.SelectableCount,
		AllowAddOption:  payload.AllowAddOption,
		EndsAt:          payload.EndsAt,
		Quiz:            payload.Quiz,
		Options:         []messagePollOption{},
	}
	if m.Poll == nil {
		return poll
	}
	poll.TotalVoters = m.Poll.TotalVoters
	for _, option := range m.Poll.Options {
		wire := messagePollOption{Index: option.Index, Name: option.Name}
		for _, voter := range option.Voters {
			wire.Voters = append(wire.Voters, messagePollVoter{
				JID:        voter.JID,
				Name:       voter.DisplayName,
				AvatarPath: voter.AvatarLocalPath,
				Timestamp:  voter.VotedAtUnix,
				FromMe:     voter.FromMe,
			})
			if voter.FromMe {
				wire.SelfVoted = true
				poll.SelfVoted = true
			}
		}
		poll.Options = append(poll.Options, wire)
	}
	return poll
}

// messageEventFromStore joins an event's fixed description with the answers
// the store holds for it.
func messageEventFromStore(m store.Message, payload *store.EventPayload) *messageEvent {
	if payload == nil {
		return nil
	}
	event := &messageEvent{
		Name:               payload.Name,
		Description:        payload.Description,
		StartsAt:           payload.StartsAt,
		EndsAt:             payload.EndsAt,
		Canceled:           payload.Canceled,
		JoinLink:           payload.JoinLink,
		Location:           payload.Location,
		ExtraGuestsAllowed: payload.ExtraGuestsAllowed,
		ScheduleCall:       payload.ScheduleCall,
		ReminderOffsetSecs: payload.ReminderOffsetSecs,
		Responders:         []messageEventResponder{},
	}
	if m.Event == nil {
		return event
	}
	event.GoingCount = m.Event.Going()
	event.SelfResponse = m.Event.SelfResponse
	event.SelfGuests = m.Event.SelfGuests
	for _, responder := range m.Event.Responders {
		event.Responders = append(event.Responders, messageEventResponder{
			JID:         responder.JID,
			Name:        responder.DisplayName,
			AvatarPath:  responder.AvatarLocalPath,
			Response:    responder.Response,
			ExtraGuests: responder.ExtraGuests,
			Timestamp:   responder.RespondedAtUnix,
			FromMe:      responder.FromMe,
		})
	}
	return event
}

func messageMediaFromStore(m store.Message) *messageMedia {
	// Text rows, unsupported tombstones and every structured kind that has
	// nothing to fetch (a poll, a contact card, a system event) carry no media
	// object at all, so nothing downstream mistakes them for a pending download.
	if !store.MessageCarriesMedia(m) {
		return nil
	}
	return &messageMedia{
		Mime:          m.MediaMimeType,
		Width:         m.MediaWidth,
		Height:        m.MediaHeight,
		Animated:      m.MediaAnimated,
		ThumbnailPath: m.MediaThumbnailLocalPath,
		Path:          m.MediaLocalPath,
		DownloadError: m.MediaDownloadError,
		SizeBytes:     m.MediaSizeBytes,
		DurationSecs:  m.MediaDurationSecs,
		Filename:      m.MediaFileName,
		PageCount:     m.MediaPageCount,
		Waveform:      waveformToWire(m.MediaWaveform),
		Played:        m.MediaPlayed,
	}
}

// waveformToWire widens the stored bytes into JSON numbers. Frontends get a
// plain array of 0-100 rather than base64 they would have to decode.
func waveformToWire(waveform []byte) []int {
	if len(waveform) == 0 {
		return nil
	}
	out := make([]int, len(waveform))
	for i, v := range waveform {
		out[i] = int(v)
	}
	return out
}

func messageReactions(reactions []store.Reaction) []messageReaction {
	if len(reactions) == 0 {
		return nil
	}
	out := make([]messageReaction, len(reactions))
	for i, r := range reactions {
		out[i] = messageReaction{
			Emoji:      r.Emoji,
			SenderID:   r.SenderID,
			SenderName: r.SenderName,
			Timestamp:  r.TimestampUnix,
			FromMe:     r.FromMe,
		}
	}
	return out
}

func messageMentions(mentions []store.MessageMention) []messageMention {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]messageMention, len(mentions))
	for i, m := range mentions {
		out[i] = messageMention{JID: m.JID, Name: m.DisplayName}
	}
	return out
}

// oneLine collapses whitespace runs into single spaces so a fallback is a
// genuine one-liner even when the source text spans multiple lines.
