package protocol

import (
	"context"
	"strings"
	"unicode/utf8"
)

type groupCreateParams struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
	Photo   string   `json:"photo_path"`
}

func (h commandHandlers) groupCreate(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupCreateParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errorf(CodeInvalidParams, "name is required")
	}
	if utf8.RuneCountInString(p.Name) > 100 {
		return nil, errorf(CodeInvalidParams, "name must be <= 100 characters")
	}
	chat, err := h.actions.CreateGroup(ctx, strings.TrimSpace(p.Name), trimStringSlice(p.Members), strings.TrimSpace(p.Photo))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"chat_id": chat.ID}, nil
}

type groupChatParams struct {
	ChatID string `json:"chat_id"`
}

func (p groupChatParams) valid() *Error {
	if strings.TrimSpace(p.ChatID) == "" {
		return errorf(CodeInvalidParams, "chat_id is required")
	}
	return nil
}

func (h commandHandlers) groupLeave(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	return nil, mapCommandError(h.actions.LeaveGroup(ctx, strings.TrimSpace(p.ChatID)))
}

type groupSetNameParams struct {
	ChatID string `json:"chat_id"`
	Name   string `json:"name"`
}

func (h commandHandlers) groupSetName(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupSetNameParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errorf(CodeInvalidParams, "name is required")
	}
	return nil, mapCommandError(h.actions.SetGroupName(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.Name)))
}

type groupSetTopicParams struct {
	ChatID      string `json:"chat_id"`
	Description string `json:"description"`
}

func (h commandHandlers) groupSetTopic(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupSetTopicParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	return nil, mapCommandError(h.actions.SetGroupDescription(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.Description)))
}

type groupSetPhotoParams struct {
	ChatID string `json:"chat_id"`
	Path   string `json:"path"`
}

func (h commandHandlers) groupSetPhoto(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupSetPhotoParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	return nil, mapCommandError(h.actions.SetGroupPhoto(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.Path)))
}

type groupInviteLinkParams struct {
	ChatID string `json:"chat_id"`
	Reset  bool   `json:"reset"`
}

func (h commandHandlers) groupInviteLink(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupInviteLinkParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	link, err := h.actions.GetGroupInviteLink(ctx, strings.TrimSpace(p.ChatID), p.Reset)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"link": link}, nil
}

type groupJoinLinkParams struct {
	Link string `json:"link"`
}

func (h commandHandlers) groupJoinLink(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupJoinLinkParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Link) == "" {
		return nil, errorf(CodeInvalidParams, "link is required")
	}
	chat, err := h.actions.JoinGroupWithLink(ctx, strings.TrimSpace(p.Link))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"chat_id": chat.ID}, nil
}

type groupMembersParams struct {
	ChatID  string   `json:"chat_id"`
	Action  string   `json:"action"`
	Members []string `json:"members"`
}

func (h commandHandlers) groupMembers(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupMembersParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "add", "remove", "promote", "demote":
	default:
		return nil, errorf(CodeInvalidParams, "action must be one of add, remove, promote, demote")
	}
	members := trimStringSlice(p.Members)
	if len(members) == 0 {
		return nil, errorf(CodeInvalidParams, "at least one member is required")
	}
	return nil, mapCommandError(h.actions.UpdateGroupMembers(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.Action), members))
}

type groupFlagParams struct {
	ChatID  string `json:"chat_id"`
	Enabled *bool  `json:"enabled"`
}

func (h commandHandlers) groupSetAnnounce(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupFlagParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Enabled == nil {
		return nil, errorf(CodeInvalidParams, "enabled is required")
	}
	return nil, mapCommandError(h.actions.SetGroupAnnounce(ctx, strings.TrimSpace(p.ChatID), *p.Enabled))
}

func (h commandHandlers) groupSetLocked(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p groupFlagParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Enabled == nil {
		return nil, errorf(CodeInvalidParams, "enabled is required")
	}
	return nil, mapCommandError(h.actions.SetGroupLocked(ctx, strings.TrimSpace(p.ChatID), *p.Enabled))
}
