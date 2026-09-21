package protocol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

const (
	// statusRingScanLimit bounds the status scan backing chat-list rings:
	// rings only ever need recent senders.
	statusRingScanLimit = 300
	// statusRingMaxAge is the status lifetime; older rows never ring.
	statusRingMaxAge = 24 * time.Hour
)

// ChatLister supplies the `chats` and `chat` views their rows. *store.DB
// implements it.
type ChatLister interface {
	ListChatsForView(ctx context.Context, filter store.ChatListFilter) ([]store.Chat, error)
	GetChatForView(ctx context.Context, chatID string) (store.Chat, error)
}

// chatSortTimeMax is larger than any real unix-seconds timestamp; subtracting
// the last-message time from it turns "most recent" into "smallest sort key",
// so bytewise-ascending order (PROTOCOL.md) renders the recency section
// newest-first. It leaves 19 decimal digits, which %020d zero-pads to a
// fixed width so the strings compare numerically.
const chatSortTimeMax = int64(1) << 62

// chatsView is the collection view over the chat list. filter/archived come
// from subscribe params; windowing, diffing and remove-on-fall-out are the
// engine's job — the session only produces ordered rows.
type chatsView struct {
	daemon   *app.Daemon
	lister   ChatLister
	statuses StatusLister
}

type chatsParams struct {
	Filter   string `json:"filter"`
	Archived bool   `json:"archived"`
	FolderID *int64 `json:"folder_id"`
}

type chatView struct {
	daemon *app.Daemon
	lister ChatLister
}

type chatParams struct {
	ChatID string `json:"chat_id"`
}

func (v chatView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p chatParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed chat params")
		}
	}
	if p.ChatID == "" {
		return nil, nil, errorf(CodeInvalidParams, "chat params must carry a chat_id")
	}
	if v.lister == nil {
		return nil, nil, errorf(CodeInternal, "chat view unavailable")
	}
	ctx, cancelCtx := context.WithCancel(context.Background())
	if _, err := v.lister.GetChatForView(ctx, p.ChatID); err != nil {
		cancelCtx()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, errorf(CodeNotFound, "no chat %q", p.ChatID)
		}
		return nil, nil, errorf(CodeInternal, "chat lookup failed: %v", err)
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &chatSession{
		chatID:       p.ChatID,
		lister:       v.lister,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

func (v chatsView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p chatsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed chats params")
		}
	}
	kind, ok := normalizeChatFilter(p.Filter)
	if !ok {
		return nil, nil, errorf(CodeInvalidParams, "filter must be one of all, direct, groups, unread, favorite")
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &chatsSession{
		lister:       v.lister,
		statuses:     v.statuses,
		filter:       store.ChatListFilter{Kind: kind, Archived: p.Archived, FolderID: p.FolderID},
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

func normalizeChatFilter(filter string) (string, bool) {
	switch filter {
	case "", "all":
		return store.ChatFilterAll, true
	case store.ChatFilterDirect:
		return store.ChatFilterDirect, true
	case store.ChatFilterGroups:
		return store.ChatFilterGroups, true
	case store.ChatFilterUnread:
		return store.ChatFilterUnread, true
	case store.ChatFilterFavorite:
		return store.ChatFilterFavorite, true
	default:
		return "", false
	}
}

type chatsSession struct {
	lister       ChatLister
	statuses     StatusLister
	filter       store.ChatListFilter
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once

	// windowMu guards windowIDs: the chat ids currently in the window, so an
	// avatar landing for a chat outside it does not re-read the whole list.
	windowMu  sync.Mutex
	windowIDs map[string]bool
}

func (s *chatsSession) noteWindow(chats []store.Chat) {
	ids := make(map[string]bool, len(chats))
	for _, c := range chats {
		ids[c.ID] = true
	}
	s.windowMu.Lock()
	s.windowIDs = ids
	s.windowMu.Unlock()
}

func (s *chatsSession) inWindow(id string) bool {
	s.windowMu.Lock()
	defer s.windowMu.Unlock()
	return s.windowIDs[id]
}

type chatSession struct {
	chatID       string
	lister       ChatLister
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

type chatItem struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	IsGroup              bool   `json:"is_group"`
	Preview              string `json:"preview"`
	LastMessageTime      int64  `json:"last_message_time"`
	LastMessageDirection string `json:"last_message_direction,omitempty"`
	LastMessageStatus    string `json:"last_message_status,omitempty"`
	Unread               int32  `json:"unread"`
	Pinned               bool   `json:"pinned"`
	PinnedOrder          uint32 `json:"pinned_order,omitempty"`
	Favorite             bool   `json:"favorite"`
	Archived             bool   `json:"archived"`
	Muted                bool   `json:"muted"`
	MuteEndTimestamp     int64  `json:"mute_end_timestamp,omitempty"`
	HistoryExhausted     bool   `json:"history_exhausted"`
	AvatarPath           string `json:"avatar_path,omitempty"`
	// StatusState is "unviewed"/"viewed" when the chat's DM sender has an
	// unexpired status, absent otherwise. Drives the status ring on the
	// avatar; tapping it opens the status viewer.
	StatusState string `json:"status_state,omitempty"`
}

// run invalidates the window whenever a daemon event may have changed a chat
// row. Items always re-reads the store, so a redundant invalidate just
// recomputes to no diff.
func (s *chatsSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if chatEventAffectsList(evt.Kind) && (evt.Kind != app.DaemonEventAvatarUpdated || s.inWindow(evt.Avatar.ID)) {
				invalidate()
			}
		}
	}
}

func (s *chatSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if chatEventAffectsList(evt.Kind) && (evt.Kind != app.DaemonEventAvatarUpdated || evt.Avatar.ID == s.chatID) {
				invalidate()
			}
		}
	}
}

// chatEventAffectsList reports whether an event kind can change a chat row at
// all. Avatar updates additionally have to name a chat the caller is showing —
// avatar fetching is bursty and demand-driven, and an unrelated one used to
// re-read and re-diff the whole window.
func chatEventAffectsList(kind app.DaemonEventKind) bool {
	switch kind {
	case app.DaemonEventResync, // re-read the store after a dropped-event gap
		app.DaemonEventNewMessage,
		app.DaemonEventChatUpdated,
		app.DaemonEventChatDeleted,
		app.DaemonEventChatCleared,
		app.DaemonEventMessageUpdated,
		app.DaemonEventMessageDeleted,
		app.DaemonEventAvatarUpdated,
		app.DaemonEventStatusChanged:
		return true
	default:
		return false
	}
}

// Items returns the first max chats in view order (all when max <= 0). The
// store applies the filter and ordering; the engine truncates to the window
// and diffs.
func (s *chatsSession) Items(max int) []Item {
	items, _ := s.ItemsErr(max)
	return items
}

func (s *chatsSession) ItemsErr(max int) ([]Item, error) {
	if s.lister == nil {
		return nil, nil
	}
	filter := s.filter
	if max > 0 {
		filter.Limit = max
	}
	chats, err := s.lister.ListChatsForView(s.ctx, filter)
	if err != nil {
		log.Printf("protocol: list chats for view: %v", err)
		return nil, err
	}
	s.noteWindow(chats)
	statusStates := s.statusStates()
	items := make([]Item, 0, len(chats))
	for _, c := range chats {
		item := chatItemFromStore(c)
		if state, ok := statusStates[c.ID]; ok {
			item.StatusState = state
		}
		items = append(items, Item{ID: c.ID, Sort: chatSort(c), Data: item})
	}
	return items, nil
}

// statusStates maps DM sender ids to their unexpired-status state
// ("unviewed" wins over "viewed"). Only statuses newer than 24h ring; the
// window is capped because rings only ever need the recent senders.
func (s *chatsSession) statusStates() map[string]string {
	states := map[string]string{}
	if s.statuses == nil {
		return states
	}
	rows, err := s.statuses.ListStatusUpdates(s.ctx, statusRingScanLimit)
	if err != nil {
		log.Printf("protocol: list statuses for chat rings: %v", err)
		return states
	}
	cutoff := time.Now().Add(-statusRingMaxAge).Unix()
	for _, st := range rows {
		if st.TimestampUnix < cutoff || st.SenderID == "" || st.SenderID == "me" {
			continue
		}
		if st.Viewed {
			if _, ok := states[st.SenderID]; !ok {
				states[st.SenderID] = "viewed"
			}
			continue
		}
		states[st.SenderID] = "unviewed"
	}
	return states
}

func (s *chatsSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

func (s *chatSession) Items(_ int) []Item {
	chat, err := s.lister.GetChatForView(s.ctx, s.chatID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("protocol: get chat for view %s: %v", s.chatID, err)
		}
		return nil
	}
	return []Item{{ID: chat.ID, Sort: objectViewSort, Data: chatItemFromStore(chat)}}
}

func (s *chatSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

func chatItemFromStore(c store.Chat) chatItem {
	return chatItem{
		ID:                   c.ID,
		Name:                 c.Name,
		IsGroup:              c.IsGroup,
		Preview:              c.LastMessage,
		LastMessageTime:      c.LastMessageTime,
		LastMessageDirection: c.LastMessageDirection,
		LastMessageStatus:    c.LastMessageStatus,
		Unread:               c.UnreadCount,
		Pinned:               c.IsPinned,
		PinnedOrder:          c.PinnedOrder,
		Favorite:             c.IsFavorite,
		Archived:             c.IsArchived,
		Muted:                c.IsMuted,
		MuteEndTimestamp:     c.MuteEndTimestamp,
		HistoryExhausted:     c.HistoryExhausted,
		AvatarPath:           c.AvatarLocalPath,
	}
}

// chatSort computes the opaque ordering key: a pinned/unpinned section prefix,
// then a recency key that sorts newest-first, then the id as a stable
// tiebreaker. Pinned chats additionally sort by pinned_order (highest first).
func chatSort(c store.Chat) string {
	if c.IsPinned {
		return fmt.Sprintf("0-%020d-%020d-%s", uint64(math.MaxUint32)-uint64(c.PinnedOrder), invChatTime(c.LastMessageTime), c.ID)
	}
	return fmt.Sprintf("1-%020d-%s", invChatTime(c.LastMessageTime), c.ID)
}

func invChatTime(t int64) int64 {
	if t < 0 {
		t = 0
	}
	if t > chatSortTimeMax {
		t = chatSortTimeMax
	}
	return chatSortTimeMax - t
}
