package protocol

import (
	"encoding/json"
	"sort"
	"sync"

	"whatevrd/internal/app"
)

// transfersView is the active media-transfer collection. Terminal success or
// failure removes the row; durable failure rendering belongs to the message
// media state (`media.download_error`).
type transfersView struct {
	daemon *app.Daemon
}

func (v transfersView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &transfersSession{
		eventsCancel: cancel,
		done:         make(chan struct{}),
		transfers:    make(map[string]app.MediaDownloadEvent),
	}
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type transfersSession struct {
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu        sync.Mutex
	transfers map[string]app.MediaDownloadEvent // message_id -> active transfer
}

type transferItem struct {
	ID            string `json:"id"`
	MessageID     string `json:"message_id"`
	ChatID        string `json:"chat_id,omitempty"`
	Direction     string `json:"direction"`
	ReceivedBytes uint64 `json:"received_bytes"`
	TotalBytes    uint64 `json:"total_bytes"`
	Error         string `json:"error,omitempty"`
}

func (s *transfersSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *transfersSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.apply(evt) {
				invalidate()
			}
		}
	}
}

func (s *transfersSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind != app.DaemonEventMediaDownloadChanged || evt.MediaDownload.MessageID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	messageID := evt.MediaDownload.MessageID
	if evt.MediaDownload.Downloading {
		prev, ok := s.transfers[messageID]
		if ok && prev == evt.MediaDownload {
			return false
		}
		s.transfers[messageID] = evt.MediaDownload
		return true
	}
	if _, ok := s.transfers[messageID]; !ok {
		return false
	}
	delete(s.transfers, messageID)
	return true
}

func (s *transfersSession) Items(max int) []Item {
	s.mu.Lock()
	transfers := make([]app.MediaDownloadEvent, 0, len(s.transfers))
	for _, transfer := range s.transfers {
		transfers = append(transfers, transfer)
	}
	s.mu.Unlock()

	sort.Slice(transfers, func(i, j int) bool { return transfers[i].MessageID < transfers[j].MessageID })
	if max > 0 && len(transfers) > max {
		transfers = transfers[:max]
	}

	items := make([]Item, 0, len(transfers))
	for _, transfer := range transfers {
		items = append(items, Item{
			ID:   transfer.MessageID,
			Sort: transfer.MessageID,
			Data: transferItem{
				ID:            transfer.MessageID,
				MessageID:     transfer.MessageID,
				ChatID:        transfer.ChatID,
				Direction:     "download",
				ReceivedBytes: transfer.ReceivedBytes,
				TotalBytes:    transfer.TotalBytes,
				Error:         transfer.ErrorText,
			},
		})
	}
	return items
}

func (s *transfersSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}
