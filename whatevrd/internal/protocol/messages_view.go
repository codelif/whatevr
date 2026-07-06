package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// MessageLister supplies the `messages` view its rows. *store.DB implements it.
type MessageLister interface {
	ListMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]store.Message, error)
}

// messagesUnboundedLimit caps an unwindowed subscription's fetch. A messages
// subscription with no `limit` is unusual (a client almost always windows a
// conversation), but the engine permits it; this bound keeps a pathological
// "no limit" from trying to LIMIT on the entire chat history at once.
const messagesUnboundedLimit = 1 << 20

// messagesView is the per-chat conversation view. It is anchored at the live
// edge: the window holds the newest N messages, new messages always arrive,
// and `extend` reaches older into the local store (fetching older history
// *from the phone* is the separate `chat.request_older` command). Anchors
// other than `latest` land in B3b.
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
	switch p.Anchor {
	case "", "latest":
		// Live-edge anchor, implemented here.
	case "unread":
		return nil, nil, errorf(CodeInvalidParams, "anchor \"unread\" not yet supported")
	default:
		// Any other value is a message-id anchor.
		return nil, nil, errorf(CodeInvalidParams, "anchor %q not yet supported", p.Anchor)
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &messagesSession{
		lister:       v.lister,
		chatID:       p.ChatID,
		eventsCancel: cancel,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type messagesSession struct {
	lister       MessageLister
	chatID       string
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

// Items returns the newest max messages of the chat. The window is the live
// edge, so the slice is ordered newest-first: the engine keeps the slice
// prefix as the window (which must therefore hold the newest messages) and
// treats a slice longer than the window as "older messages remain locally".
// Each item still carries an ascending timestamp sort key, so the client
// orders the conversation oldest→newest regardless of arrival order.
func (s *messagesSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	msgs, err := s.lister.ListMessages(context.Background(), s.chatID, limit, "")
	if err != nil {
		log.Printf("protocol: list messages for view: %v", err)
		return nil
	}
	// ListMessages yields the newest `limit` messages in ascending order;
	// reverse into newest-first for the prefix window.
	items := make([]Item, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		items = append(items, Item{ID: m.ID, Sort: messageSort(m), Data: messageItemFromStore(m)})
	}
	return items
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
