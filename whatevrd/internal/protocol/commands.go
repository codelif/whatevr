package protocol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	appstore "whatevrd/internal/store"
)

// CommandActions is the daemon/WA seam used by protocol commands. *wa.Client
// implements it; tests and the conformance fixture can provide a small fake.
type CommandActions interface {
	FrontendSessionStarted(string)
	FrontendSessionEnded(string)
	FrontendSessionStateChanged(string, bool, string)

	Reconnect(context.Context) error
	Logout(context.Context) error

	MarkChatReadUpTo(context.Context, string, string) (appstore.Chat, error)
	SetChatPinned(context.Context, string, bool) (appstore.Chat, error)
	SetChatArchived(context.Context, string, bool) (appstore.Chat, error)
	SetChatMuted(context.Context, string, bool, time.Duration) (appstore.Chat, error)
	SetChatPresence(context.Context, string, bool) error
	RequestOlderMessages(context.Context, string) (bool, error)
	EnsureDirectChat(context.Context, string) (appstore.Chat, error)
}

// RegisterDaemonCommands registers the C1 command surface from PROTOCOL.md.
func RegisterDaemonCommands(s *Server, actions CommandActions) {
	s.commandActions = actions
	cmd := commandHandlers{actions: actions}
	s.RegisterCommand("session.update", cmd.sessionUpdate)
	s.RegisterCommand("daemon.reconnect", cmd.daemonReconnect)
	s.RegisterCommand("account.logout", cmd.accountLogout)
	s.RegisterCommand("chat.mark_read", cmd.chatMarkRead)
	s.RegisterCommand("chat.pin", cmd.chatPin)
	s.RegisterCommand("chat.archive", cmd.chatArchive)
	s.RegisterCommand("chat.mute", cmd.chatMute)
	s.RegisterCommand("chat.typing", cmd.chatTyping)
	s.RegisterCommand("chat.request_older", cmd.chatRequestOlder)
	s.RegisterCommand("chat.ensure_direct", cmd.chatEnsureDirect)
}

// RegisterCommand makes a command request method available. Safe during setup;
// commands are normally registered immediately after Start and before clients
// connect.
func (s *Server) RegisterCommand(name string, h handlerFunc) {
	s.handlers[name] = h
}

type commandHandlers struct {
	actions CommandActions
}

func (h commandHandlers) requireActions() *Error {
	if h.actions == nil {
		return errorf(CodeInternal, "command actions are not available")
	}
	return nil
}

func (c *conn) ensureFrontendSession(actions CommandActions) {
	if c.sessionActive {
		return
	}
	if c.sessionID == "" {
		c.sessionID = "protocol-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	actions.FrontendSessionStarted(c.sessionID)
	c.sessionActive = true
}

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
	h.actions.FrontendSessionStateChanged(c.sessionID, *p.Focused, strings.TrimSpace(p.ActiveChatID))
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

func (h commandHandlers) accountLogout(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	if err := rejectNonEmptyParams(req.Params); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.Logout(context.Background()))
}

type chatIDParams struct {
	ChatID string `json:"chat_id"`
}

func (p chatIDParams) valid() *Error {
	if strings.TrimSpace(p.ChatID) == "" {
		return errorf(CodeInvalidParams, "chat_id is required")
	}
	return nil
}

type chatMarkReadParams struct {
	ChatID        string `json:"chat_id"`
	UpToMessageID string `json:"up_to_message_id"`
}

func (h commandHandlers) chatMarkRead(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatMarkReadParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.UpToMessageID) == "" {
		return nil, errorf(CodeInvalidParams, "up_to_message_id is required")
	}
	_, err := h.actions.MarkChatReadUpTo(context.Background(), strings.TrimSpace(p.ChatID), strings.TrimSpace(p.UpToMessageID))
	return nil, mapCommandError(err)
}

type chatPinParams struct {
	ChatID string `json:"chat_id"`
	Pinned *bool  `json:"pinned"`
}

func (h commandHandlers) chatPin(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatPinParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Pinned == nil {
		return nil, errorf(CodeInvalidParams, "pinned is required")
	}
	_, err := h.actions.SetChatPinned(context.Background(), strings.TrimSpace(p.ChatID), *p.Pinned)
	return nil, mapCommandError(err)
}

type chatArchiveParams struct {
	ChatID   string `json:"chat_id"`
	Archived *bool  `json:"archived"`
}

func (h commandHandlers) chatArchive(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatArchiveParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Archived == nil {
		return nil, errorf(CodeInvalidParams, "archived is required")
	}
	_, err := h.actions.SetChatArchived(context.Background(), strings.TrimSpace(p.ChatID), *p.Archived)
	return nil, mapCommandError(err)
}

type chatMuteParams struct {
	ChatID       string `json:"chat_id"`
	Muted        *bool  `json:"muted"`
	DurationSecs int64  `json:"duration_secs"`
}

func (h commandHandlers) chatMute(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatMuteParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Muted == nil {
		return nil, errorf(CodeInvalidParams, "muted is required")
	}
	if p.DurationSecs < 0 {
		return nil, errorf(CodeInvalidParams, "duration_secs must be non-negative")
	}
	_, err := h.actions.SetChatMuted(context.Background(), strings.TrimSpace(p.ChatID), *p.Muted, time.Duration(p.DurationSecs)*time.Second)
	return nil, mapCommandError(err)
}

type chatTypingParams struct {
	ChatID    string `json:"chat_id"`
	Composing *bool  `json:"composing"`
}

func (h commandHandlers) chatTyping(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatTypingParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Composing == nil {
		return nil, errorf(CodeInvalidParams, "composing is required")
	}
	return nil, mapCommandError(h.actions.SetChatPresence(context.Background(), strings.TrimSpace(p.ChatID), *p.Composing))
}

func (h commandHandlers) chatRequestOlder(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	requested, err := h.actions.RequestOlderMessages(context.Background(), strings.TrimSpace(p.ChatID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"requested": requested}, nil
}

type ensureDirectParams struct {
	JID string `json:"jid"`
}

func (h commandHandlers) chatEnsureDirect(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p ensureDirectParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.JID) == "" {
		return nil, errorf(CodeInvalidParams, "jid is required")
	}
	chat, err := h.actions.EnsureDirectChat(context.Background(), strings.TrimSpace(p.JID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"chat_id": chat.ID}, nil
}

func decodeParams(raw json.RawMessage, out any) *Error {
	if len(raw) == 0 {
		return errorf(CodeInvalidParams, "params are required")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errorf(CodeInvalidParams, "malformed params")
	}
	return nil
}

func rejectNonEmptyParams(raw json.RawMessage) *Error {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return errorf(CodeInvalidParams, "malformed params")
	}
	if len(obj) != 0 {
		return errorf(CodeInvalidParams, "params must be empty")
	}
	return nil
}

func mapCommandError(err error) *Error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errorf(CodeNotFound, "%v", err)
	}
	if st, ok := grpcstatus.FromError(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			return errorf(CodeInvalidParams, "%s", st.Message())
		case codes.NotFound:
			return errorf(CodeNotFound, "%s", st.Message())
		case codes.FailedPrecondition:
			return errorf(CodeNotLoggedIn, "%s", st.Message())
		case codes.Unavailable:
			return errorf(CodeNotConnected, "%s", st.Message())
		case codes.AlreadyExists:
			return errorf(CodeAlreadyExists, "%s", st.Message())
		case codes.ResourceExhausted, codes.Aborted:
			return errorf(CodeRejected, "%s", st.Message())
		case codes.Internal:
			return errorf(CodeInternal, "%s", st.Message())
		}
	}
	return errorf(CodeInternal, "%v", err)
}
