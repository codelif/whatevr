package protocol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
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
			chatID:       p.ChatID,
			eventsCancel: cancel,
			ctx:          ctx,
			cancelCtx:    cancelCtx,
			done:         make(chan struct{}),
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
		if _, err := v.lister.ListMessagesAround(ctx, p.ChatID, 1, p.Anchor); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, errorf(CodeNotFound, "no message %q in chat %q", p.Anchor, p.ChatID)
			}
			log.Printf("protocol: messages anchor %q: %v", p.Anchor, err)
			return "", nil, errorf(CodeInternal, "resolve message anchor")
		}
		return p.Anchor, map[string]any{"anchor_id": p.Anchor}, nil
	}
}

// messagesChatFeed is the plumbing both session shapes share: it watches
// daemon events and invalidates the window whenever a message in this chat may
// have changed. Items always re-reads the store, so a spurious invalidate just
// recomputes to no diff.
type messagesChatFeed struct {
	lister       MessageLister
	chatID       string
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once

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
		// Dropped-event gap: re-read the store (Items is authoritative).
		return true
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
	if s.lister == nil {
		return nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	msgs, err := s.lister.ListMessages(s.ctx, s.chatID, limit, "")
	if err != nil {
		log.Printf("protocol: list latest messages for view: %v", err)
		return nil
	}
	reverseMessages(msgs) // newest-first for the prefix window
	s.noteAvatarSubjects(msgs)
	items := make([]Item, 0, len(msgs))
	for _, m := range msgs {
		items = append(items, messageWireItem(m))
	}
	return items
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

func (s *anchoredMessagesSession) Items(int) []Item {
	if s.lister == nil {
		return nil
	}
	s.mu.Lock()
	olderN, newerN, live := s.olderReach, s.newerReach, s.atLiveEdge
	s.mu.Unlock()
	ctx := s.ctx

	anchor, err := s.lister.GetMessage(ctx, s.anchorID)
	if err != nil {
		// The anchor was validated at subscribe; if it is later deleted the
		// window collapses to empty rather than erroring the live view.
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("protocol: anchored messages get anchor %q: %v", s.anchorID, err)
		}
		return nil
	}

	// Older frontier: the olderN messages nearest the anchor on the older side
	// (ascending). Fetch one extra to detect exhaustion, then keep the newest.
	older, err := s.lister.ListMessages(ctx, s.chatID, olderN+1, s.anchorID)
	if err != nil {
		log.Printf("protocol: anchored messages older frontier: %v", err)
		return nil
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
		return nil
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
		items = append(items, messageWireItem(m))
	}
	return items
}

// messageWireItem projects a stored message into a view Item: stable id, the
// ascending timestamp sort key (render order), and the wire shape.
func messageWireItem(m store.Message) Item {
	return Item{ID: m.ID, Sort: messageSort(m), Data: messageItemFromStore(m)}
}

// reverseMessages flips a slice in place; the store returns ascending pages
// that the live-edge window renders newest-first.
func reverseMessages(msgs []store.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

// messageSort is the opaque ordering key: timestamp then the arrival-order
// tiebreaker (the row's sort sequence), zero-padded so the strings compare
// numerically. Ascending bytewise order renders oldest-first.
func messageSort(m store.Message) string {
	ts := m.TimestampUnix
	if ts < 0 {
		ts = 0
	}
	seq := m.SortSeq
	if seq < 0 {
		seq = 0
	}
	return fmt.Sprintf("%020d-%020d", ts, seq)
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
	Media       *messageMedia     `json:"media,omitempty"`
}

type messageSender struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	AvatarPath string `json:"avatar_path,omitempty"`
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
}

func messageItemFromStore(m store.Message) messageItem {
	kind := messageKind(m)
	item := messageItem{
		ID:          m.ID,
		ChatID:      m.ChatID,
		Kind:        kind,
		Fallback:    messageFallback(m, kind),
		Text:        m.Text,
		Sender:      messageSender{ID: m.SenderID, Name: m.SenderName, AvatarPath: m.SenderAvatarLocalPath},
		Timestamp:   m.TimestampUnix,
		Direction:   m.Direction,
		Status:      m.Status,
		Edited:      m.IsEdited,
		Revoked:     m.IsRevoked,
		Starred:     m.IsStarred,
		Forwarded:   m.IsForwarded,
		PinnedUntil: m.PinnedUntil,
		Reactions:   messageReactions(m.Reactions),
		Mentions:    messageMentions(m.Mentions),
		Media:       messageMediaFromStore(m),
	}
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
	return mediaKindToWire(m.MediaKind)
}

func mediaKindToWire(mediaKind string) string {
	switch mediaKind {
	case "":
		return "text"
	case store.MediaKindImage:
		return "image"
	case store.MediaKindSticker:
		return "sticker"
	case store.MediaKindUnsupported:
		return "unsupported"
	default:
		return mediaKind
	}
}

// messageFallback is the one-line human rendering a frontend shows for any kind
// it does not implement (and the natural preview for the ones it does).
func messageFallback(m store.Message, kind string) string {
	if m.IsRevoked {
		return "This message was deleted"
	}
	caption := oneLine(m.Text)
	switch kind {
	case "image":
		if caption != "" {
			return caption
		}
		return "📷 Photo"
	case "sticker":
		return "🎨 Sticker"
	case "unsupported":
		if caption != "" {
			return caption
		}
		return "Unsupported message"
	default: // text and unknown kinds
		return caption
	}
}

func messageMediaFromStore(m store.Message) *messageMedia {
	switch m.MediaKind {
	case store.MediaKindImage, store.MediaKindSticker:
	default:
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
	}
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
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
