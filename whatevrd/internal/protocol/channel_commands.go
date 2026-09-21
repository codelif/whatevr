package protocol

import (
	"context"
	"strings"
)

// channels.refresh refetches the followed-channel directory.
func (h commandHandlers) channelsRefresh(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	if err := rejectNonEmptyParams(req.Params); err != nil {
		return nil, err
	}
	channels, err := h.actions.RefreshChannels(ctx)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"count": len(channels)}, nil
}

type channelIDParams struct {
	ChannelID string `json:"channel_id"`
}

func (p channelIDParams) valid() *Error {
	if strings.TrimSpace(p.ChannelID) == "" {
		return errorf(CodeInvalidParams, "channel_id is required")
	}
	return nil
}

// channel.follow follows a channel by JID.
func (h commandHandlers) channelFollow(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p channelIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.FollowChannel(ctx, strings.TrimSpace(p.ChannelID)))
}

type channelFollowLinkParams struct {
	Invite string `json:"invite"`
}

// channel.follow_link follows a channel from an invite code or link.
func (h commandHandlers) channelFollowLink(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p channelFollowLinkParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Invite) == "" {
		return nil, errorf(CodeInvalidParams, "invite is required")
	}
	channel, err := h.actions.FollowChannelByInvite(ctx, strings.TrimSpace(p.Invite))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"channel_id": channel.ID}, nil
}

// channel.unfollow leaves a channel.
func (h commandHandlers) channelUnfollow(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p channelIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.UnfollowChannel(ctx, strings.TrimSpace(p.ChannelID)))
}

type channelMuteParams struct {
	ChannelID string `json:"channel_id"`
	Muted     *bool  `json:"muted"`
}

// channel.mute mutes or unmutes a channel.
func (h commandHandlers) channelMute(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p channelMuteParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	if p.Muted == nil {
		return nil, errorf(CodeInvalidParams, "muted is required")
	}
	return nil, mapCommandError(h.actions.SetChannelMuted(ctx, strings.TrimSpace(p.ChannelID), *p.Muted))
}

func (p channelMuteParams) valid() *Error {
	if strings.TrimSpace(p.ChannelID) == "" {
		return errorf(CodeInvalidParams, "channel_id is required")
	}
	return nil
}

type channelMarkViewedParams struct {
	ChannelID string  `json:"channel_id"`
	ServerIDs []int64 `json:"server_ids"`
}

// channel.mark_viewed marks channel messages viewed up to their server IDs.
func (h commandHandlers) channelMarkViewed(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p channelMarkViewedParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChannelID) == "" {
		return nil, errorf(CodeInvalidParams, "channel_id is required")
	}
	if len(p.ServerIDs) == 0 {
		return nil, errorf(CodeInvalidParams, "at least one server id is required")
	}
	return nil, mapCommandError(h.actions.MarkChannelViewed(ctx, strings.TrimSpace(p.ChannelID), p.ServerIDs))
}

type channelReactParams struct {
	ChannelID string `json:"channel_id"`
	ServerID  int64  `json:"server_id"`
	Emoji     string `json:"emoji"`
}

func (h commandHandlers) channelReact(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p channelReactParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChannelID) == "" || p.ServerID <= 0 {
		return nil, errorf(CodeInvalidParams, "channel_id and positive server_id are required")
	}
	return nil, mapCommandError(h.actions.ReactToChannelMessage(ctx, strings.TrimSpace(p.ChannelID), p.ServerID, p.Emoji))
}
