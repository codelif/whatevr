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

// StatusLister supplies the `status` view its rows. *store.DB implements it.
type StatusLister interface {
	ListStatusUpdates(ctx context.Context, limit int) ([]store.StatusUpdate, error)
}

// statusView is the contact-status (stories) feed: a live-edge window over
// status_updates, newest first. Rows reuse the message media facts where they
// exist so a photo status renders from the same fields a chat photo would.
// Sender avatars resolve through the same display-name seam as typing.
type statusView struct {
	daemon   *app.Daemon
	lister   StatusLister
	resolver SenderDisplayer
}

func (v statusView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &statusSession{
		lister:       v.lister,
		resolver:     v.resolver,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type statusSession struct {
	lister       StatusLister
	resolver     SenderDisplayer
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *statusSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			switch evt.Kind {
			case app.DaemonEventStatusChanged, app.DaemonEventResync:
				invalidate()
			}
		}
	}
}

// Items returns the newest `max` statuses, newest first. `extend older`
// pages back; statuses expire after 24h server-side, so deep windows are
// short by nature.
func (s *statusSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	limit := max
	if limit <= 0 {
		limit = messagesUnboundedLimit
	}
	rows, err := s.lister.ListStatusUpdates(s.ctx, limit)
	if err != nil {
		log.Printf("protocol: list statuses for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(rows))
	for _, st := range rows {
		item := statusItemFromStore(st)
		if s.resolver != nil && st.SenderID != "" && st.SenderID != "me" {
			if _, avatar, err := s.resolver.SenderDisplay(s.ctx, st.SenderID); err == nil {
				item.Sender.AvatarPath = avatar
			}
		}
		items = append(items, Item{
			ID:   st.ID,
			Sort: newestFirstSort(statusSortSeed(st)),
			Data: item,
		})
	}
	return items
}

func (s *statusSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

// statusSortSeed adapts a status row to the newest-first sorter shared with
// message views.
func statusSortSeed(st store.StatusUpdate) store.Message {
	return store.Message{TimestampUnix: st.TimestampUnix, ID: st.ID}
}

type statusItem struct {
	ID        string        `json:"id"`
	Sender    messageSender `json:"sender"`
	Timestamp int64         `json:"timestamp"`
	Kind      string        `json:"kind"`
	Fallback  string        `json:"fallback"`
	Text      string        `json:"text,omitempty"`
	TextBG    uint32        `json:"text_bg,omitempty"`
	TextFont  int32         `json:"text_font,omitempty"`
	Viewed    bool          `json:"viewed,omitempty"`
	Media     *messageMedia `json:"media,omitempty"`
}

func statusItemFromStore(st store.StatusUpdate) statusItem {
	item := statusItem{
		ID:        st.ID,
		Sender:    messageSender{ID: st.SenderID, Name: st.SenderName},
		Timestamp: st.TimestampUnix,
		Kind:      st.Kind,
		Text:      st.Text,
		TextBG:    st.TextBG,
		TextFont:  st.TextFont,
		Viewed:    st.Viewed,
	}
	if item.Kind == "" {
		item.Kind = "text"
	}
	item.Fallback = statusFallback(st)
	if st.MediaKind != "" {
		item.Media = &messageMedia{
			Mime:          st.MediaMimeType,
			Path:          st.MediaLocalPath,
			SizeBytes:     st.MediaSizeBytes,
			DurationSecs:  st.MediaDurationSecs,
			Filename:      st.MediaFileName,
			ThumbnailPath: st.MediaThumbnailLocalPath,
			Width:         st.MediaWidth,
			Height:        st.MediaHeight,
		}
	}
	return item
}

func statusFallback(st store.StatusUpdate) string {
	if caption := strings.Join(strings.Fields(st.Text), " "); caption != "" {
		return caption
	}
	switch st.MediaKind {
	case store.MediaKindImage:
		return "📷 Photo status"
	case store.MediaKindVideo:
		return "🎥 Video status"
	case store.MediaKindGIF:
		return "🎞️ GIF status"
	case store.MediaKindVoice:
		return "🎤 Voice status"
	case store.MediaKindAudio:
		return "🎵 Audio status"
	default:
		return "Status update"
	}
}

// StatusSenderLister supplies the `status.kept` and `status.muted` views
// their rows. *store.DB implements it.
type StatusSenderLister interface {
	ListKeptStatusSenders(ctx context.Context) ([]string, error)
	ListMutedStatusSenders(ctx context.Context) ([]string, error)
}

// statusKeptView lists the sender ids with status keep enabled: one row per
// sender, `{id: sender_id}`. The Status tab reads it to decide which contacts
// grow an archived section for their expired statuses. Local store read, so
// the first load is synchronous; later keep flips arrive as StatusChanged.
type statusKeptView struct {
	daemon *app.Daemon
	lister StatusSenderLister
}

func (v statusKeptView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &statusKeptSession{
		lister:       v.lister,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type statusKeptSession struct {
	lister       StatusSenderLister
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *statusKeptSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			switch evt.Kind {
			case app.DaemonEventStatusChanged, app.DaemonEventResync:
				invalidate()
			}
		}
	}
}

// Items returns one row per kept sender. `max` is ignored: the set is tiny.
func (s *statusKeptSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	rows, err := s.lister.ListKeptStatusSenders(s.ctx)
	if err != nil {
		log.Printf("protocol: list kept status senders for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(rows))
	for _, id := range rows {
		items = append(items, Item{
			ID:   id,
			Sort: id,
			Data: map[string]any{"id": id},
		})
	}
	return items
}

func (s *statusKeptSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

// statusMutedView lists the sender ids with status mute enabled: one row per
// sender, `{id: sender_id}`. The Status tab reads it to decide which contacts
// collect under the collapsed Muted section instead of the main list. Local
// store read, so the first load is synchronous; later mute flips arrive as
// StatusChanged.
type statusMutedView struct {
	daemon *app.Daemon
	lister StatusSenderLister
}

func (v statusMutedView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &statusMutedSession{
		lister:       v.lister,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type statusMutedSession struct {
	lister       StatusSenderLister
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *statusMutedSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			switch evt.Kind {
			case app.DaemonEventStatusChanged, app.DaemonEventResync:
				invalidate()
			}
		}
	}
}

// Items returns one row per muted sender. `max` is ignored: the set is tiny.
func (s *statusMutedSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	rows, err := s.lister.ListMutedStatusSenders(s.ctx)
	if err != nil {
		log.Printf("protocol: list muted status senders for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(rows))
	for _, id := range rows {
		items = append(items, Item{
			ID:   id,
			Sort: id,
			Data: map[string]any{"id": id},
		})
	}
	return items
}

func (s *statusMutedSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}
