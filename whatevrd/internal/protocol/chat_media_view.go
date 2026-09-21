package protocol

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// ChatMediaLister supplies the `chat_media` and `chat_links` views their
// rows. *store.DB implements it.
type ChatMediaLister interface {
	ListChatMediaMessages(ctx context.Context, chatID string, limit int, beforeMessageID string, kinds []string) ([]store.Message, error)
	ListChatLinkMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]store.Message, error)
}

// chatMediaView is a chat's media gallery: a live-edge prefix window over the
// photos, videos, voice notes, audio files, documents, polls, contacts and
// locations in one chat, newest first. `kinds` narrows to a subset (empty
// means everything). It reuses the `messages` item shape verbatim, so the
// gallery renders the same rows the conversation does, and a download landing
// shows up here as an ordinary upsert with `media.path` set.
type chatMediaView struct {
	daemon *app.Daemon
	lister ChatMediaLister
}

type chatMediaParams struct {
	ChatID string   `json:"chat_id"`
	Kinds  []string `json:"kinds"`
	// Kind is the legacy singular filter; Kinds wins when both are set.
	// Tolerated so a singular sender degrades to unfiltered rather than
	// silently showing everything.
	Kind string `json:"kind"`
}

func (v chatMediaView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p chatMediaParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed chat_media params")
		}
	}
	if p.ChatID == "" {
		return nil, nil, errorf(CodeInvalidParams, "chat_media params must carry a chat_id")
	}
	kinds, err := filterGalleryKinds(p.Kinds)
	if err != nil {
		return nil, nil, err
	}
	if len(kinds) == 0 && strings.TrimSpace(p.Kind) != "" {
		kinds, err = filterGalleryKinds([]string{p.Kind})
		if err != nil {
			return nil, nil, err
		}
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &chatMediaSession{
		lister:       v.lister,
		chatID:       p.ChatID,
		kinds:        kinds,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

// filterGalleryKinds validates a kinds filter against the gallery set. Empty
// means everything; anything else must name real gallery kinds.
func filterGalleryKinds(kinds []string) ([]string, *Error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	known := map[string]bool{}
	for _, kind := range store.GalleryMediaKinds {
		known[kind] = true
	}
	out := make([]string, 0, len(kinds))
	seen := map[string]bool{}
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" || seen[kind] {
			continue
		}
		if !known[kind] {
			return nil, errorf(CodeInvalidParams, "unknown gallery kind %q", kind)
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out, nil
}

type chatMediaSession struct {
	lister       ChatMediaLister
	chatID       string
	kinds        []string
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *chatMediaSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

// eventAffects reports whether an event may have changed this window's rows.
// New media arrives as DaemonEventNewMessage; a download completing, a revoke
// (which strips the media and drops the row) and a star flip all ride
// DaemonEventMessageUpdated. Items always re-reads, so a spurious hit just
// diffs to nothing.
func (s *chatMediaSession) eventAffects(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync, app.DaemonEventHistoryBackfilled:
		return true
	case app.DaemonEventNewMessage, app.DaemonEventMessageUpdated:
		return evt.Message.ChatID == s.chatID
	case app.DaemonEventMessageDeleted, app.DaemonEventChatDeleted:
		return evt.DeletedChatID == s.chatID
	case app.DaemonEventChatCleared:
		return evt.Chat.ID == s.chatID
	default:
		return false
	}
}

// Items returns the newest `max` media rows, each carrying a newest-first sort
// key. `extend older` grows the window back through the chat's history.
func (s *chatMediaSession) Items(max int) []Item {
	items, _ := s.ItemsErr(max)
	return items
}

func (s *chatMediaSession) ItemsErr(max int) ([]Item, error) {
	if s.lister == nil {
		return nil, nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	rows, err := s.lister.ListChatMediaMessages(s.ctx, s.chatID, limit, "", s.kinds)
	if err != nil {
		log.Printf("protocol: list chat media for view: %v", err)
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, m := range rows {
		items = append(items, Item{
			ID:   m.ID,
			Sort: newestFirstSort(m),
			Data: messageItemFromStore(m),
		})
	}
	return items, nil
}

func (s *chatMediaSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}
