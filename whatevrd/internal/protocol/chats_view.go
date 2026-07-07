package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// ChatLister supplies the `chats` view its rows. *store.DB implements it.
type ChatLister interface {
	ListChatsForView(ctx context.Context, filter store.ChatListFilter) ([]store.Chat, error)
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
	daemon *app.Daemon
	lister ChatLister
}

type chatsParams struct {
	Filter   string `json:"filter"`
	Archived bool   `json:"archived"`
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
		return nil, nil, errorf(CodeInvalidParams, "filter must be one of all, direct, groups")
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &chatsSession{
		lister:       v.lister,
		filter:       store.ChatListFilter{Kind: kind, Archived: p.Archived},
		eventsCancel: cancel,
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
	default:
		return "", false
	}
}

type chatsSession struct {
	lister       ChatLister
	filter       store.ChatListFilter
	eventsCancel func()
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
	Archived             bool   `json:"archived"`
	Muted                bool   `json:"muted"`
	MuteEndTimestamp     int64  `json:"mute_end_timestamp,omitempty"`
	HistoryExhausted     bool   `json:"history_exhausted"`
	AvatarPath           string `json:"avatar_path,omitempty"`
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
			if chatEventAffectsList(evt.Kind) {
				invalidate()
			}
		}
	}
}

func chatEventAffectsList(kind app.DaemonEventKind) bool {
	switch kind {
	case app.DaemonEventResync, // re-read the store after a dropped-event gap
		app.DaemonEventNewMessage,
		app.DaemonEventChatUpdated,
		app.DaemonEventChatDeleted,
		app.DaemonEventChatCleared,
		app.DaemonEventMessageUpdated,
		app.DaemonEventMessageDeleted,
		app.DaemonEventAvatarUpdated:
		return true
	default:
		return false
	}
}

// Items returns the first max chats in view order (all when max <= 0). The
// store applies the filter and ordering; the engine truncates to the window
// and diffs.
func (s *chatsSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	filter := s.filter
	if max > 0 {
		filter.Limit = max
	}
	chats, err := s.lister.ListChatsForView(context.Background(), filter)
	if err != nil {
		log.Printf("protocol: list chats for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(chats))
	for _, c := range chats {
		items = append(items, Item{ID: c.ID, Sort: chatSort(c), Data: chatItemFromStore(c)})
	}
	return items
}

func (s *chatsSession) Close() {
	s.closeOnce.Do(func() {
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
