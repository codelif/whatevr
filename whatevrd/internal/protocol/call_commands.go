package protocol

import (
	"context"
	"strings"
)

// call.reject declines the latest ringing call in a chat. Rejecting a call
// that already ended is a silent no-op; answering from the desktop is not
// possible (no media stack upstream), so reject + "answer on your phone" is
// the whole surface.
func (h commandHandlers) callReject(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupChatParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.RejectCall(ctx, strings.TrimSpace(p.ChatID)))
}
