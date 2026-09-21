package protocol

import (
	"context"
	"strings"
)

// community.subgroups lists the sub-groups linked under a community: the
// directory members browse in WhatsApp's community home. Each entry carries
// the group JID plus its display name when known locally.
func (h commandHandlers) communitySubgroups(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	groups, err := h.actions.ListCommunitySubgroups(ctx, strings.TrimSpace(p.ChatID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	out := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		out = append(out, map[string]any{"id": group.ID, "name": group.Name})
	}
	return map[string]any{"groups": out}, nil
}

type communityLinkParams struct {
	CommunityID string `json:"community_id"`
	GroupID     string `json:"group_id"`
}

func (p communityLinkParams) valid() *Error {
	if strings.TrimSpace(p.CommunityID) == "" {
		return errorf(CodeInvalidParams, "community_id is required")
	}
	if strings.TrimSpace(p.GroupID) == "" {
		return errorf(CodeInvalidParams, "group_id is required")
	}
	return nil
}

// community.link attaches an existing group under a community (admins).
func (h commandHandlers) communityLink(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p communityLinkParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.LinkCommunityGroup(ctx, strings.TrimSpace(p.CommunityID), strings.TrimSpace(p.GroupID)))
}

// community.unlink detaches a sub-group from its community (admins).
func (h commandHandlers) communityUnlink(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p communityLinkParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.UnlinkCommunityGroup(ctx, strings.TrimSpace(p.CommunityID), strings.TrimSpace(p.GroupID)))
}
