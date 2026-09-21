package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// ChannelLister supplies the `channels` view its rows. *store.DB implements
// it.
type ChannelLister interface {
	ListChannels(ctx context.Context) ([]store.Channel, error)
}

// ChannelActions fetches live channel messages. *wa.Client implements it.
type ChannelActions interface {
	GetChannelMessages(ctx context.Context, channelID string, count int, before int64) ([]app.ChannelMessage, error)
}

// channelActionsFrom narrows the daemon actions seam to the channel surface.
// Tests and the fixture pass nil when the views they exercise do not touch
// it; the view errors instead of panicking.
func channelActionsFrom(actions DaemonActions) ChannelActions {
	if actions == nil {
		return nil
	}
	if channelActions, ok := any(actions).(ChannelActions); ok {
		return channelActions
	}
	return nil
}

// channelsView is the followed-channels directory: one item per channel from
// the store cache, refreshed by `channels.refresh` (and follow/unfollow).
type channelsView struct {
	daemon *app.Daemon
	lister ChannelLister
}

func (v channelsView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &channelsSession{
		lister:       v.lister,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type channelsSession struct {
	lister       ChannelLister
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *channelsSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			switch evt.Kind {
			case app.DaemonEventChannelsChanged, app.DaemonEventResync:
				invalidate()
			}
		}
	}
}

type channelItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Followers   int    `json:"followers"`
	Verified    bool   `json:"verified,omitempty"`
	Muted       bool   `json:"muted,omitempty"`
}

// Items returns followed channels, most-followed first.
func (s *channelsSession) Items(max int) []Item {
	if s.lister == nil {
		return nil
	}
	rows, err := s.lister.ListChannels(s.ctx)
	if err != nil {
		log.Printf("protocol: list channels for view: %v", err)
		return nil
	}
	if max > 0 && len(rows) > max {
		rows = rows[:max]
	}
	items := make([]Item, 0, len(rows))
	for _, channel := range rows {
		items = append(items, Item{
			ID:   channel.ID,
			Sort: channel.ID,
			Data: channelItem{
				ID:          channel.ID,
				Name:        channel.Name,
				Description: channel.Description,
				Followers:   channel.Followers,
				Verified:    channel.Verified,
				Muted:       channel.Muted,
			},
		})
	}
	return items
}

func (s *channelsSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

// channelMessagesView is one channel's recent messages, newest first,
// fetched live on every fill (never stored). `extend older` pages back by
// server id.
type channelMessagesView struct {
	daemon  *app.Daemon
	actions ChannelActions
}

type channelMessagesParams struct {
	ChannelID string `json:"channel_id"`
}

func (v channelMessagesView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p channelMessagesParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed channel_messages params")
		}
	}
	if p.ChannelID == "" {
		return nil, nil, errorf(CodeInvalidParams, "channel_messages params must carry a channel_id")
	}
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "channel messages unavailable")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	ctx, cancelCtx := context.WithCancel(context.Background())
	s := &channelMessagesSession{
		actions:      v.actions,
		channelID:    p.ChannelID,
		eventsCancel: cancel,
		ctx:          ctx,
		cancelCtx:    cancelCtx,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type channelMessagesSession struct {
	actions      ChannelActions
	channelID    string
	eventsCancel func()
	ctx          context.Context
	cancelCtx    context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once

	// Own-window state (DirectionalSession): rows newest-first across every
	// page fetched so far, keyed by server id; oldest is the paging cursor
	// (0 until the first fill). Live-edge refreshes merge by id so an
	// invalidate never loses scrolled-back rows.
	mu        sync.Mutex
	window    []Item
	byID      map[string]bool
	oldest    int64
	exhausted bool
	filled    bool
}

const channelMessagesPageSize = 30

func (s *channelMessagesSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			switch evt.Kind {
			case app.DaemonEventChannelsChanged, app.DaemonEventResync:
				s.refreshLiveEdge()
				invalidate()
			}
		}
	}
}

// refreshLiveEdge merges the newest page into the window (new posts appear on
// top; everything already held stays where it is).
func (s *channelMessagesSession) refreshLiveEdge() {
	if s.actions == nil {
		return
	}
	rows, err := s.actions.GetChannelMessages(s.ctx, s.channelID, channelMessagesPageSize, 0)
	if err != nil {
		log.Printf("protocol: refresh channel messages for view: %v", err)
		return
	}
	s.merge(rows)
}

// merge folds fetched rows into the window, newest-first, deduplicating by
// id and tracking the oldest server id held.
func (s *channelMessagesSession) merge(rows []app.ChannelMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = make(map[string]bool)
	}
	for _, msg := range rows {
		id := channelMessageID(s.channelID, msg.ServerID)
		if s.byID[id] {
			continue
		}
		s.byID[id] = true
		s.window = append(s.window, Item{
			ID:   id,
			Sort: channelMessageSort(msg.ServerID),
			Data: channelMessageItem{
				ID:        id,
				ChannelID: s.channelID,
				ServerID:  msg.ServerID,
				Timestamp: msg.Timestamp,
				Kind:      msg.Kind,
				Text:      msg.Text,
				Fallback:  msg.Fallback,
				Views:     msg.Views,
			},
		})
		if s.oldest == 0 || msg.ServerID < s.oldest {
			s.oldest = msg.ServerID
		}
	}
	sort.Slice(s.window, func(i, j int) bool { return s.window[i].Sort > s.window[j].Sort })
	s.filled = true
}

// ExtendWindow pages older messages behind the cursor. Newer is a no-op:
// the live edge refreshes on channel events instead.
func (s *channelMessagesSession) ExtendWindow(direction string, count int) {
	if direction != "older" || s.actions == nil {
		return
	}
	s.mu.Lock()
	if s.exhausted || !s.filled {
		s.mu.Unlock()
		return
	}
	before := s.oldest
	s.mu.Unlock()
	if count <= 0 || count > 100 {
		count = channelMessagesPageSize
	}
	rows, err := s.actions.GetChannelMessages(s.ctx, s.channelID, count, before)
	if err != nil {
		log.Printf("protocol: page channel messages for view: %v", err)
		return
	}
	if len(rows) < count {
		s.mu.Lock()
		s.exhausted = true
		s.mu.Unlock()
	}
	s.merge(rows)
}

// Exhausted reports whether the older frontier is spent.
func (s *channelMessagesSession) Exhausted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exhausted
}

// Items reports the whole held window; the first call fills the live edge.
func (s *channelMessagesSession) Items(max int) []Item {
	s.mu.Lock()
	filled := s.filled
	s.mu.Unlock()
	if !filled {
		s.refreshLiveEdge()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, len(s.window))
	copy(out, s.window)
	return out
}

type channelMessageItem struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	ServerID  int64  `json:"server_id"`
	Timestamp int64  `json:"timestamp"`
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	Fallback  string `json:"fallback"`
	Views     int    `json:"views,omitempty"`
}

func (s *channelMessagesSession) Close() {
	s.closeOnce.Do(func() {
		s.cancelCtx()
		close(s.done)
		s.eventsCancel()
	})
}

func channelMessageID(channelID string, serverID int64) string {
	return fmt.Sprintf("%s:%d", channelID, serverID)
}

// channelMessageSort orders newest (largest server id) first under bytewise
// ascending comparison by inverting the id.
func channelMessageSort(serverID int64) string {
	return fmt.Sprintf("%020d", ^uint64(serverID))
}
