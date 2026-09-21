package protocol

import (
	"context"
	"strings"
	"time"
)

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

func (h commandHandlers) chatMarkRead(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	_, err := h.actions.MarkChatReadUpTo(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.UpToMessageID))
	return nil, mapCommandError(err)
}

// chat.mark_all_read marks every unread message read in every chat (local
// rows, badges, and upstream read receipts), returning how many chats had
// unread.
func (h commandHandlers) chatMarkAllRead(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	if err := rejectNonEmptyParams(req.Params); err != nil {
		return nil, err
	}
	marked, err := h.actions.MarkAllChatsRead(ctx)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"marked_chats": marked}, nil
}

type chatPinParams struct {
	ChatID string `json:"chat_id"`
	Pinned *bool  `json:"pinned"`
}

func (h commandHandlers) chatPin(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	_, err := h.actions.SetChatPinned(ctx, strings.TrimSpace(p.ChatID), *p.Pinned)
	return nil, mapCommandError(err)
}

type chatFavoriteParams struct {
	ChatID   string `json:"chat_id"`
	Favorite *bool  `json:"favorite"`
}

func (h commandHandlers) chatFavorite(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatFavoriteParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Favorite == nil {
		return nil, errorf(CodeInvalidParams, "favorite is required")
	}
	_, err := h.actions.SetChatFavorite(ctx, strings.TrimSpace(p.ChatID), *p.Favorite)
	return nil, mapCommandError(err)
}

type chatArchiveParams struct {
	ChatID   string `json:"chat_id"`
	Archived *bool  `json:"archived"`
}

func (h commandHandlers) chatArchive(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	_, err := h.actions.SetChatArchived(ctx, strings.TrimSpace(p.ChatID), *p.Archived)
	return nil, mapCommandError(err)
}

type chatMuteParams struct {
	ChatID       string `json:"chat_id"`
	Muted        *bool  `json:"muted"`
	DurationSecs int64  `json:"duration_secs"`
}

func (h commandHandlers) chatMute(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	if p.DurationSecs > maxCommandDurationSecs {
		return nil, errorf(CodeInvalidParams, "duration_secs is too large")
	}
	_, err := h.actions.SetChatMuted(ctx, strings.TrimSpace(p.ChatID), *p.Muted, time.Duration(p.DurationSecs)*time.Second)
	return nil, mapCommandError(err)
}

type chatExportParams struct {
	ChatID string `json:"chat_id"`
	Path   string `json:"path"`
}

// chatExport writes the chat transcript to path in the official WhatsApp
// .txt export format. The frontend picks the destination with a save dialog.
func (h commandHandlers) chatExport(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatExportParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.Path) == "" {
		return nil, errorf(CodeInvalidParams, "path is required")
	}
	path, err := h.actions.ExportChat(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.Path))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"path": path}, nil
}

type chatTypingParams struct {
	ChatID    string `json:"chat_id"`
	Composing *bool  `json:"composing"`
}

func (h commandHandlers) chatTyping(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	return nil, mapCommandError(h.actions.SetChatPresence(ctx, strings.TrimSpace(p.ChatID), *p.Composing))
}

func (h commandHandlers) chatRequestOlder(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	requested, err := h.actions.RequestOlderMessages(ctx, strings.TrimSpace(p.ChatID))
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
