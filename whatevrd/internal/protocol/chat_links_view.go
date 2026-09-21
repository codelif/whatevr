package protocol

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"whatevrd/internal/app"
)

// chatLinksView is a chat's link gallery: messages containing URLs, newest
// first. Rows are ordinary `messages` items (with chat_name), so tapping one
// jumps into its conversation context like starred rows do.
type chatLinksView struct {
	daemon *app.Daemon
	lister ChatMediaLister
}

type chatLinksParams struct {
	ChatID string `json:"chat_id"`
}

func (v chatLinksView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p chatLinksParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed chat_links params")
		}
	}
	if p.ChatID == "" {
		return nil, nil, errorf(CodeInvalidParams, "chat_links params must carry a chat_id")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &chatLinksSession{
		lister:       v.lister,
		chatID:       p.ChatID,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type chatLinksSession struct {
	lister       ChatMediaLister
	chatID       string
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *chatLinksSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

// eventAffects mirrors the media gallery: any message change in this chat
// may add, remove or retarget a link.
func (s *chatLinksSession) eventAffects(evt app.DaemonEvent) bool {
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

// Items returns the newest `max` link messages, newest first.
func (s *chatLinksSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	rows, err := s.lister.ListChatLinkMessages(s.ctx, s.chatID, limit, "")
	if err != nil {
		log.Printf("protocol: list chat links for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(rows))
	for _, m := range rows {
		items = append(items, Item{
			ID:   m.ID,
			Sort: newestFirstSort(m),
			Data: messageItemFromStore(m),
		})
	}
	return items
}

func (s *chatLinksSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}
