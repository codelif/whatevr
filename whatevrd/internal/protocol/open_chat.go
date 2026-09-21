package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// MarkChatRead is the notification action seam. Inline notification replies
// reuse the normal send action; marking read falls back to opening the chat when
// no message horizon is available in the notification payload.
func (s *Server) MarkChatRead(chatID string) bool {
	if s.commandActions == nil || strings.TrimSpace(chatID) == "" {
		return false
	}
	_, err := s.commandActions.MarkChatRead(context.Background(), chatID)
	return err == nil
}

// ReplyToChat sends notification text through the same daemon action used by
// the protocol's send.text command.
func (s *Server) ReplyToChat(chatID, text string) bool {
	if s.commandActions == nil || strings.TrimSpace(chatID) == "" || strings.TrimSpace(text) == "" {
		return false
	}
	_, err := s.commandActions.SendText(context.Background(), chatID, text, "", nil)
	return err == nil
}

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
// connection-directed open_chat event to the most recently updated active
// frontend session (focusing preferred, all-active as fallback) so a
// notification click can raise an existing window instead of cold-starting
// a duplicate.
func (s *Server) OpenChat(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false
	}
	line, err := json.Marshal(openChatEvent{Event: "open_chat", ChatID: chatID})
	if err != nil {
		return false
	}
	return s.dispatchConnectionDirected(line)
}
