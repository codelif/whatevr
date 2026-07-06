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
// ListMessages backs the live-edge (`latest`) window; ListMessagesAround backs
// anchored (`unread` / message-id) windows as a balanced neighborhood around a
// target; ListMessagesAroundUnread resolves the oldest-unread anchor id from a
// chat's unread count; GetChat supplies that unread count.
type MessageLister interface {
	ListMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]store.Message, error)
	ListMessagesAround(ctx context.Context, chatID string, limit int, targetMessageID string) ([]store.Message, error)
	ListMessagesAroundUnread(ctx context.Context, chatID string, limit int, unreadCount int) ([]store.Message, string, error)
	GetChat(ctx context.Context, chatID string) (store.Chat, error)
}

// messagesUnboundedLimit caps an unwindowed subscription's fetch. A messages
// subscription with no `limit` is unusual (a client almost always windows a
// conversation), but the engine permits it; this bound keeps a pathological
// "no limit" from trying to LIMIT on the entire chat history at once.
const messagesUnboundedLimit = 1 << 20

// messagesView is the per-chat conversation view. With the default `latest`
// anchor it is anchored at the live edge: the window holds the newest N
// messages, new messages always arrive, and `extend` reaches older into the
// local store. The `unread` and message-id anchors instead position the
// window around a mid-history anchor (the oldest unread message, or a named
// message a frontend is jumping to); the window is a balanced neighborhood
// that `extend` widens both directions. Fetching older history *from the
// phone* is the separate `chat.request_older` command.
//
// The anchored window reuses A2's prefix-window engine unchanged: the session
// returns items ordered by proximity to the anchor (the "importance" order the
// engine keeps as its prefix), while each item's ascending timestamp sort key
// drives render order — the same slice-order/sort-key split the `latest`
// window uses, just keyed on distance-from-anchor instead of recency.
type messagesView struct {
	daemon *app.Daemon
	lister MessageLister
}

type messagesParams struct {
	ChatID string `json:"chat_id"`
	Anchor string `json:"anchor"`
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

	anchorID, meta, verr := v.resolveAnchor(p)
	if verr != nil {
		return nil, nil, verr
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &messagesSession{
		lister:       v.lister,
		chatID:       p.ChatID,
		anchorID:     anchorID,
		eventsCancel: cancel,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, meta, nil
}

// resolveAnchor turns the `anchor` param into a fixed anchor message id (empty
// for the live edge) and the subscribe meta. The anchor is pinned once here so
// the window stays put as messages come and go and the reported `anchor_id`
// never drifts. `unread` with nothing unread (or an unresolvable count)
// degrades to the live edge with no `anchor_id`, exactly as if `latest` were
// requested.
func (v messagesView) resolveAnchor(p messagesParams) (string, map[string]any, *Error) {
	switch p.Anchor {
	case "", "latest":
		return "", nil, nil
	case "unread":
		ctx := context.Background()
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
		if _, err := v.lister.ListMessagesAround(context.Background(), p.ChatID, 1, p.Anchor); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, errorf(CodeNotFound, "no message %q in chat %q", p.Anchor, p.ChatID)
			}
			log.Printf("protocol: messages anchor %q: %v", p.Anchor, err)
			return "", nil, errorf(CodeInternal, "resolve message anchor")
		}
		return p.Anchor, map[string]any{"anchor_id": p.Anchor}, nil
	}
}

type messagesSession struct {
	lister       MessageLister
	chatID       string
	anchorID     string // empty = live-edge window; else balanced around this id
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once
}

// run invalidates the window whenever a daemon event may have changed a
// message in this chat. Items always re-reads the store, so a spurious
// invalidate just recomputes to no diff.
func (s *messagesSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.eventAffectsChat(evt) {
				invalidate()
			}
		}
	}
}

func (s *messagesSession) eventAffectsChat(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventNewMessage, app.DaemonEventMessageUpdated:
		return evt.Message.ChatID == s.chatID
	case app.DaemonEventMessageDeleted, app.DaemonEventChatDeleted:
		return evt.DeletedChatID == s.chatID
	case app.DaemonEventChatCleared:
		return evt.Chat.ID == s.chatID
	case app.DaemonEventHistoryBackfilled:
		return evt.HistorySync.ChatID == s.chatID
	case app.DaemonEventAvatarUpdated:
		// An avatar change may be a participant of this chat; re-reading is
		// cheap and diffs to nothing when no sender row here changed.
		return true
	default:
		return false
	}
}

// Items returns the max most-relevant messages of the chat in the slice order
// the engine keeps as its prefix window, each carrying an ascending timestamp
// sort key so the client renders the conversation oldest→newest regardless of
// arrival order. For the live edge (no anchor) "relevance" is recency, so the
// slice is newest-first and the prefix is the newest N. For an anchored
// window "relevance" is proximity to the anchor, so the prefix is a balanced
// neighborhood the anchor sits in the middle of; `extend` widens it both ways.
func (s *messagesSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	msgs, err := s.orderedMessages(limit)
	if err != nil {
		log.Printf("protocol: list messages for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(msgs))
	for _, m := range msgs {
		items = append(items, Item{ID: m.ID, Sort: messageSort(m), Data: messageItemFromStore(m)})
	}
	return items
}

// orderedMessages fetches the window for the current anchor and returns it in
// engine-prefix order (most relevant first). The live edge reverses the
// store's ascending page into newest-first; an anchored window fetches the
// balanced neighborhood around the anchor and reorders it by distance from the
// anchor so the engine's prefix trim always keeps a contiguous run centered on
// the anchor.
func (s *messagesSession) orderedMessages(limit int) ([]store.Message, error) {
	ctx := context.Background()
	if s.anchorID == "" {
		msgs, err := s.lister.ListMessages(ctx, s.chatID, limit, "")
		if err != nil {
			return nil, err
		}
		reverseMessages(msgs)
		return msgs, nil
	}
	msgs, err := s.lister.ListMessagesAround(ctx, s.chatID, limit, s.anchorID)
	if err != nil {
		// The anchor was validated at subscribe time; a message deleted since
		// then leaves the window momentarily empty rather than erroring the
		// live view.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return orderByProximity(msgs, s.anchorID), nil
}

// orderByProximity reorders a contiguous, ascending run of messages that
// contains the anchor into "closest to the anchor first": the anchor, then its
// nearest newer and older neighbors alternating outward. The engine keeps the
// closest `window` of these as its prefix; because the input is contiguous and
// we only ever drop from the far ends, that prefix is itself a contiguous run
// the anchor sits inside — no render gaps.
func orderByProximity(msgs []store.Message, anchorID string) []store.Message {
	idx := -1
	for i := range msgs {
		if msgs[i].ID == anchorID {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Anchor not in the returned window (shouldn't happen); fall back to
		// the store's ascending order rather than dropping rows.
		return msgs
	}
	out := make([]store.Message, 0, len(msgs))
	out = append(out, msgs[idx])
	for lo, hi := idx-1, idx+1; lo >= 0 || hi < len(msgs); lo, hi = lo-1, hi+1 {
		if hi < len(msgs) {
			out = append(out, msgs[hi])
		}
		if lo >= 0 {
			out = append(out, msgs[lo])
		}
	}
	return out
}

// reverseMessages flips a slice in place; the store returns ascending pages
// that the live-edge window renders newest-first.
func reverseMessages(msgs []store.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

func (s *messagesSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
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
