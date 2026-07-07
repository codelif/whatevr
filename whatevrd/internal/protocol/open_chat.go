package protocol

import (
	"encoding/json"
	"strings"
	"time"
)

type openChatEvent struct {
	Event  string `json:"event"`
	ChatID string `json:"chat_id"`
}

type frontendRouteState struct {
	active    bool
	focused   bool
	updatedAt time.Time
}

func (c *conn) routeState() frontendRouteState {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	return frontendRouteState{
		active:    c.sessionActive,
		focused:   c.sessionFocused,
		updatedAt: c.sessionUpdatedAt,
	}
}

func (c *conn) enqueueOpenChat(line []byte) {
	// Connection-directed events have no subscription id, so they travel as
	// ordinary connection-level frames: never coalesced and never tied to a view.
	c.q.push(line, false)
}

// OpenChat implements notify.ChatOpener for protocol frontends. It sends the
// connection-directed open_chat event to the most recently updated focused
// frontend session; if no focused protocol frontend exists, it falls back to the
// most recently updated live protocol session so a notification click can raise
// an existing (currently unfocused) window instead of cold-starting a duplicate.
func (s *Server) OpenChat(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false
	}
	line, err := json.Marshal(openChatEvent{Event: "open_chat", ChatID: chatID})
	if err != nil {
		return false
	}

	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	var focused *conn
	var focusedAt time.Time
	var fallback *conn
	var fallbackAt time.Time
	for _, c := range conns {
		state := c.routeState()
		if !state.active {
			continue
		}
		if state.focused {
			if focused == nil || state.updatedAt.After(focusedAt) {
				focused = c
				focusedAt = state.updatedAt
			}
			continue
		}
		if fallback == nil || state.updatedAt.After(fallbackAt) {
			fallback = c
			fallbackAt = state.updatedAt
		}
	}

	target := focused
	if target == nil {
		target = fallback
	}
	if target == nil {
		return false
	}
	target.enqueueOpenChat(line)
	return true
}
