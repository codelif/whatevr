package protocol

import (
	"context"
	"strings"
	"time"
)

type sessionUpdateParams struct {
	Focused      *bool  `json:"focused"`
	ActiveChatID string `json:"active_chat_id"`
}

func (h commandHandlers) sessionUpdate(c *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sessionUpdateParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if p.Focused == nil {
		return nil, errorf(CodeInvalidParams, "focused is required")
	}
	c.ensureFrontendSession(h.actions)
	activeChatID := strings.TrimSpace(p.ActiveChatID)
	c.sessionMu.Lock()
	id := c.sessionID
	c.sessionFocused = *p.Focused
	c.sessionActiveChatID = activeChatID
	c.sessionUpdatedAt = time.Now()
	c.sessionMu.Unlock()
	h.actions.FrontendSessionStateChanged(id, *p.Focused, activeChatID)
	return nil, nil
}

func (h commandHandlers) daemonReconnect(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	if err := rejectNonEmptyParams(req.Params); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.Reconnect(context.Background()))
}

func (h commandHandlers) accountLogout(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	if err := rejectNonEmptyParams(req.Params); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.Logout(ctx))
}
