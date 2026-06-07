package rpc

import (
	"sync"

	"whatevrd/internal/rpc/pb"
)

// SessionBus is the daemon→frontend push channel. Each live HoldSession stream
// registers a buffered channel here; the daemon fans events (currently just
// "open this chat") out to every connected frontend without blocking on slow
// or stalled streams.
type SessionBus struct {
	mu       sync.Mutex
	sessions map[string]chan *pb.FrontendSessionEvent
}

func NewSessionBus() *SessionBus {
	return &SessionBus{sessions: make(map[string]chan *pb.FrontendSessionEvent)}
}

// Register returns a receive channel for sessionID. The caller must Unregister
// the same id when its stream ends.
func (b *SessionBus) Register(sessionID string) <-chan *pb.FrontendSessionEvent {
	ch := make(chan *pb.FrontendSessionEvent, 8)
	b.mu.Lock()
	if existing, ok := b.sessions[sessionID]; ok {
		// A reconnect under the same id supersedes the stale channel.
		close(existing)
	}
	b.sessions[sessionID] = ch
	b.mu.Unlock()
	return ch
}

func (b *SessionBus) Unregister(sessionID string) {
	b.mu.Lock()
	if ch, ok := b.sessions[sessionID]; ok {
		delete(b.sessions, sessionID)
		close(ch)
	}
	b.mu.Unlock()
}

// OpenChat asks every connected frontend to surface chatID and reports whether
// at least one frontend received the request. A false return means no live
// frontend exists, so the caller should cold-start one instead.
func (b *SessionBus) OpenChat(chatID string) bool {
	event := &pb.FrontendSessionEvent{OpenChat: &pb.OpenChat{ChatId: chatID}}

	b.mu.Lock()
	defer b.mu.Unlock()
	delivered := false
	for _, ch := range b.sessions {
		select {
		case ch <- event:
			delivered = true
		default:
			// Drop rather than block; a frontend wedged enough to fill an
			// 8-deep buffer would not service the request anyway.
		}
	}
	return delivered
}
