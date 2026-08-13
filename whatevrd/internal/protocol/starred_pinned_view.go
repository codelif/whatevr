package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// StarredPinnedLister supplies the `starred` and `pinned` views their rows.
// *store.DB implements it. ListStarredMessages backs the windowed `starred`
// view (newest-first, optionally scoped to one chat, each row carrying its
// chat's display name); ListPinnedMessages backs the unwindowed per-chat
// `pinned` banner (currently-pinned, unexpired).
type StarredPinnedLister interface {
	ListStarredMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]store.StarredMessage, error)
	ListPinnedMessages(ctx context.Context, chatID string) ([]store.Message, error)
}

// --- starred ------------------------------------------------------------

// starredView is the starred-messages page: a live-edge prefix window over the
// user's starred messages, newest first, optionally scoped to one chat. It
// reuses the `messages` item shape plus a `chat_name` for the cross-chat view.
// Starring/unstarring (here or on another device) both surface as a
// DaemonEventMessageUpdated, off which the window re-reads the store, so it
// stays in sync with stars made elsewhere, exactly as a view should.
type starredView struct {
	daemon *app.Daemon
	lister StarredPinnedLister
}

type starredParams struct {
	ChatID string `json:"chat_id"`
}

// starredItem is a starred row: the whole `messages` item plus the chat's
// display name (for labeling rows in the cross-chat view). The embedded
// messageItem's fields, including `id`, promote to the top-level object.
type starredItem struct {
	messageItem
	ChatName string `json:"chat_name,omitempty"`
}

func (v starredView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p starredParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed starred params")
		}
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &starredSession{lister: v.lister, chatID: p.ChatID, eventsCancel: cancel, ctx: ctx, cancelCtx: cancelCtx, done: make(chan struct{})}
	go s.run(events, invalidate)
	return s, nil, nil
}

type starredSession struct {
	lister       StarredPinnedLister
	chatID       string // "" spans all chats
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *starredSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.eventAffects(evt) {
				invalidate()
			}
		}
	}
}

// eventAffects reports whether an event may have changed this window's rows. A
// star flip rides DaemonEventMessageUpdated; a starred message can also vanish
// via delete-for-me or a chat delete/clear. For the cross-chat view (chatID
// "") any chat's event counts; otherwise it is scoped. Items always re-reads,
// so a spurious hit just diffs to nothing.
func (s *starredSession) eventAffects(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync:
		return true
	case app.DaemonEventMessageUpdated:
		return s.chatID == "" || evt.Message.ChatID == s.chatID
	case app.DaemonEventMessageDeleted, app.DaemonEventChatDeleted:
		return s.chatID == "" || evt.DeletedChatID == s.chatID
	case app.DaemonEventChatCleared:
		return s.chatID == "" || evt.Chat.ID == s.chatID
	case app.DaemonEventAvatarUpdated:
		// A sender/chat avatar refresh may touch a starred row; re-reading is
		// cheap and diffs to nothing when no visible row changed.
		return true
	default:
		return false
	}
}

// Items returns the newest `max` starred rows slice-ordered newest-first (the
// engine keeps the prefix = the newest), each carrying a newest-first sort key
// so generic clients render the starred page in the same order. `extend older`
// grows the window back into older stars.
func (s *starredSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	rows, err := s.lister.ListStarredMessages(s.ctx, s.chatID, limit, "")
	if err != nil {
		log.Printf("protocol: list starred messages for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(rows))
	for _, sm := range rows {
		items = append(items, Item{
			ID:   sm.ID,
			Sort: newestFirstSort(sm.Message),
			Data: starredItem{messageItem: messageItemFromStore(sm.Message), ChatName: sm.ChatName},
		})
	}
	return items
}

const newestFirstSortMax = int64(1) << 62

// newestFirstSort is the sort key for every newest-first message window
// (`starred`, `chat_media`): newest message timestamp first, then newest rowid
// first for same-second arrivals.
//
// F27: the store records only is_starred, not a star time, so `starred` is
// ordered by the message's own timestamp, not when it was starred. A
// consequence is that starring an old message places it deep in the list rather
// than at the top, so it can fall outside a small window until the window is
// extended. Recording a star time would need a schema migration; ordering by
// message timestamp is the documented behavior (see PROTOCOL.md `starred`).
func newestFirstSort(m store.Message) string {
	return fmt.Sprintf("%020d-%020d-%s", invNewestFirst(m.TimestampUnix), invNewestFirst(m.SortSeq), m.ID)
}

func invNewestFirst(v int64) int64 {
	if v < 0 {
		v = 0
	}
	if v > newestFirstSortMax {
		v = newestFirstSortMax
	}
	return newestFirstSortMax - v
}

func (s *starredSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

// --- pinned -------------------------------------------------------------

// pinnedView is a chat's pinned-message banner: an unwindowed collection of the
// currently-pinned, unexpired messages in one chat, oldest pin first. A pin or
// unpin rides DaemonEventMessageUpdated; expiry has no event, so the session
// arms a timer for the soonest pinned_until and re-reads when it fires (the now
// expired row falls out of the store query and the engine emits `remove`).
type pinnedView struct {
	daemon *app.Daemon
	lister StarredPinnedLister
}

type pinnedParams struct {
	ChatID string `json:"chat_id"`
}

func (v pinnedView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p pinnedParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed pinned params")
		}
	}
	if p.ChatID == "" {
		return nil, nil, errorf(CodeInvalidParams, "pinned params must carry a chat_id")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &pinnedSession{lister: v.lister, chatID: p.ChatID, eventsCancel: cancel, ctx: ctx, cancelCtx: cancelCtx, invalidate: invalidate, done: make(chan struct{})}
	go s.run(events, invalidate)
	return s, nil, nil
}

type pinnedSession struct {
	lister       StarredPinnedLister
	chatID       string
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	invalidate   func()
	done         chan struct{}
	closeOnce    sync.Once

	mu          sync.Mutex
	expiryTimer *time.Timer
	closed      bool
}

func (s *pinnedSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.eventAffects(evt) {
				invalidate()
			}
		}
	}
}

func (s *pinnedSession) eventAffects(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync:
		return true
	case app.DaemonEventMessageUpdated:
		return evt.Message.ChatID == s.chatID
	case app.DaemonEventMessageDeleted, app.DaemonEventChatDeleted:
		return evt.DeletedChatID == s.chatID
	case app.DaemonEventChatCleared:
		return evt.Chat.ID == s.chatID
	case app.DaemonEventAvatarUpdated:
		return true
	default:
		return false
	}
}

// Items returns the chat's currently-pinned rows (unexpired) ordered oldest pin
// first, and arms the expiry timer for the soonest pinned_until so an expiring
// pin drops out even with no daemon event.
func (s *pinnedSession) Items(int) []Item {
	if s.lister == nil {
		return nil
	}
	rows, err := s.lister.ListPinnedMessages(s.ctx, s.chatID)
	if err != nil {
		log.Printf("protocol: list pinned messages for view: %v", err)
		return nil
	}
	s.armExpiry(rows)
	items := make([]Item, 0, len(rows))
	for _, m := range rows {
		items = append(items, Item{
			ID:   m.ID,
			Sort: pinnedSort(m),
			Data: messageItemFromStore(m),
		})
	}
	return items
}

// pinnedSort orders rows by pin time then rowid, matching the store's
// oldest-pin-first banner order.
func pinnedSort(m store.Message) string {
	return fmt.Sprintf("%020d-%020d", pinnedAtOrZero(m.PinnedAt), pinnedSeqOrZero(m.SortSeq))
}

func pinnedAtOrZero(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func pinnedSeqOrZero(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// armExpiry schedules a re-read just after the soonest pinned_until among the
// current rows, so an expiring pin falls out of the view without a daemon
// event. Rescheduled on every Items; a nil/empty set disarms.
func (s *pinnedSession) armExpiry(rows []store.Message) {
	var soonest int64
	for _, m := range rows {
		if soonest == 0 || m.PinnedUntil < soonest {
			soonest = m.PinnedUntil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiryTimer != nil {
		s.expiryTimer.Stop()
		s.expiryTimer = nil
	}
	// Items (which calls armExpiry) can race Close; without this a timer armed
	// after Close would never be stopped (a bounded leak) and could fire post-
	// close. Once closed, stay disarmed. F26.
	if s.closed || soonest == 0 {
		return
	}
	// Fire a touch after expiry so the store's `pinned_until > now` filter has
	// certainly crossed the boundary.
	delay := time.Until(time.Unix(soonest, 0)) + 250*time.Millisecond
	if delay < 0 {
		delay = 0
	}
	s.expiryTimer = time.AfterFunc(delay, s.invalidate)
}

func (s *pinnedSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
		s.mu.Lock()
		s.closed = true
		if s.expiryTimer != nil {
			s.expiryTimer.Stop()
			s.expiryTimer = nil
		}
		s.mu.Unlock()
	})
}
