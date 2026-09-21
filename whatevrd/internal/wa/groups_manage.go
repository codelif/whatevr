package wa

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// This file implements the group.* management commands: create, leave,
// rename, re-describe, photo, invite links, membership and flags. Reads
// (info card, member list) already exist via the `group` views; everything
// here mutates through whatsmeow and then refreshes the stored
// participants/name so receipts, @-mentions and the info card stay correct.

func (c *Client) requireGroupJID(ctx context.Context, chatID string) (types.JID, error) {
	jid, err := types.ParseJID(strings.TrimSpace(chatID))
	if err != nil || jid.User == "" {
		return types.JID{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id")
	}
	jid = c.normalizeJIDForChat(ctx, jid)
	if jid.Server != types.GroupServer {
		return types.JID{}, app.NewCommandError(app.CommandErrorInvalidArgument, "not a group chat")
	}
	return jid, nil
}

func (c *Client) requireConnectedClient() (*whatsmeow.Client, error) {
	client := c.currentClient()
	if client == nil {
		return nil, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return nil, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	}
	return client, nil
}

func (c *Client) parseMemberJIDs(ctx context.Context, raw []string) ([]types.JID, error) {
	members := make([]types.JID, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		jid, err := types.ParseJID(s)
		if err != nil || jid.User == "" {
			return nil, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid member jid %q", s)
		}
		if jid.Server == types.GroupServer {
			return nil, app.NewCommandError(app.CommandErrorInvalidArgument, "member jid %q must be a user, not a group", s)
		}
		members = append(members, c.normalizeJIDForChat(ctx, jid.ToNonAD()))
	}
	return members, nil
}

// refreshGroupAfterChange re-reads participants and the subject after a
// mutation so the local store (and every view over it) reflects what the
// server accepted.
func (c *Client) refreshGroupAfterChange(ctx context.Context, chatJID types.JID) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}
	info, err := client.GetGroupInfo(ctx, chatJID)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warnf("Failed to refresh group %s after change: %v", chatJID, err)
		}
		return
	}
	if info == nil {
		return
	}
	c.storeGroupParticipants(ctx, chatJID, info.Participants)
	if name := strings.TrimSpace(info.Name); name != "" {
		c.ensureOrUpdateGroupName(ctx, chatJID, name)
	}
	c.refreshGroupInfoLive(ctx, chatJID, "")
}

// CreateGroup creates a group with an optional member list and photo, and
// ensures its chat row so it appears in the chat list immediately.
func (c *Client) CreateGroup(ctx context.Context, name string, memberJIDs []string, photoPath string) (appstore.Chat, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return appstore.Chat{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "group name is required")
	}
	members, err := c.parseMemberJIDs(ctx, memberJIDs)
	if err != nil {
		return appstore.Chat{}, err
	}
	info, err := client.CreateGroup(ctx, whatsmeow.ReqCreateGroup{Name: name, Participants: members})
	if err != nil {
		return appstore.Chat{}, err
	}
	if photoPath = strings.TrimSpace(photoPath); photoPath != "" {
		if err := c.setGroupPhoto(ctx, client, info.JID, photoPath); err != nil {
			c.log.Warnf("Group %s created but photo set failed: %v", info.JID, err)
		}
	}
	chatID := c.normalizeJIDForChat(ctx, info.JID).String()
	chat, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, appstore.ChatNameSourceGroup, true)
	if err != nil {
		return appstore.Chat{}, err
	}
	c.storeGroupParticipants(ctx, info.JID, info.Participants)
	c.daemon.PublishChatUpdated(toDaemonChat(chat))
	c.refreshGroupAfterChange(ctx, info.JID)
	return chat, nil
}

// LeaveGroup leaves a group. The row stays (history is kept); the composer
// locks itself via the group view's membership state on next refresh.
func (c *Client) LeaveGroup(ctx context.Context, chatID string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return err
	}
	return client.LeaveGroup(ctx, chatJID)
}

// SetGroupName renames a group.
func (c *Client) SetGroupName(ctx context.Context, chatID, name string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "group name is required")
	}
	if err := client.SetGroupName(ctx, chatJID, strings.TrimSpace(name)); err != nil {
		return err
	}
	c.refreshGroupAfterChange(ctx, chatJID)
	return nil
}

// SetGroupDescription updates a group's description.
func (c *Client) SetGroupDescription(ctx context.Context, chatID, description string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return err
	}
	if err := client.SetGroupDescription(ctx, chatJID, strings.TrimSpace(description)); err != nil {
		return err
	}
	c.refreshGroupInfoLive(ctx, chatJID, "")
	return nil
}

// SetGroupPhoto sets (or, with an empty path, clears) a group's photo.
func (c *Client) SetGroupPhoto(ctx context.Context, chatID, photoPath string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return err
	}
	if err := c.setGroupPhoto(ctx, client, chatJID, strings.TrimSpace(photoPath)); err != nil {
		return err
	}
	c.refreshGroupInfoLive(ctx, chatJID, "")
	return nil
}

func (c *Client) setGroupPhoto(ctx context.Context, client *whatsmeow.Client, chatJID types.JID, photoPath string) error {
	if photoPath == "" {
		_, err := client.SetGroupPhoto(ctx, chatJID, nil)
		return err
	}
	data, _, _, _, _, err := readOutboundMedia(photoPath, MediaSendOptions{Kind: "image"})
	if err != nil {
		return err
	}
	_, err = client.SetGroupPhoto(ctx, chatJID, data)
	return err
}

// GetGroupInviteLink returns the invite link, resetting it first when asked.
func (c *Client) GetGroupInviteLink(ctx context.Context, chatID string, reset bool) (string, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return "", err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return "", err
	}
	return client.GetGroupInviteLink(ctx, chatJID, reset)
}

// JoinGroupWithLink joins a group from an invite link or bare code, and
// ensures its chat row.
func (c *Client) JoinGroupWithLink(ctx context.Context, codeOrLink string) (appstore.Chat, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return appstore.Chat{}, err
	}
	code := strings.TrimSpace(codeOrLink)
	if code == "" {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invite link or code is required")
	}
	for _, prefix := range []string{"https://chat.whatsapp.com/", "http://chat.whatsapp.com/", "chat.whatsapp.com/"} {
		code = strings.TrimPrefix(code, prefix)
	}
	code = strings.TrimSpace(code)
	chatJID, err := client.JoinGroupWithLink(ctx, code)
	if err != nil {
		return appstore.Chat{}, err
	}
	chatID := c.normalizeJIDForChat(ctx, chatJID).String()
	chat, err := c.store.EnsureChatWithNameSource(ctx, chatID, "", appstore.ChatNameSourceRaw, true)
	if err != nil {
		return appstore.Chat{}, err
	}
	c.daemon.PublishChatUpdated(toDaemonChat(chat))
	c.refreshGroupAfterChange(ctx, chatJID)
	return chat, nil
}

// UpdateGroupMembers adds, removes, promotes or demotes members. Action is
// one of "add", "remove", "promote", "demote".
func (c *Client) UpdateGroupMembers(ctx context.Context, chatID, action string, memberJIDs []string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return err
	}
	var change whatsmeow.ParticipantChange
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add":
		change = whatsmeow.ParticipantChangeAdd
	case "remove":
		change = whatsmeow.ParticipantChangeRemove
	case "promote":
		change = whatsmeow.ParticipantChangePromote
	case "demote":
		change = whatsmeow.ParticipantChangeDemote
	default:
		return app.NewCommandError(app.CommandErrorInvalidArgument, "action must be one of add, remove, promote, demote")
	}
	members, err := c.parseMemberJIDs(ctx, memberJIDs)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "at least one member jid is required")
	}
	if _, err := client.UpdateGroupParticipants(ctx, chatJID, members, change); err != nil {
		return err
	}
	c.refreshGroupAfterChange(ctx, chatJID)
	return nil
}

// SetGroupAnnounce toggles admins-only sending.
func (c *Client) SetGroupAnnounce(ctx context.Context, chatID string, announce bool) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return err
	}
	if err := client.SetGroupAnnounce(ctx, chatJID, announce); err != nil {
		return err
	}
	c.refreshGroupInfoLive(ctx, chatJID, "")
	return nil
}

// SetGroupLocked toggles admins-only group-info editing.
func (c *Client) SetGroupLocked(ctx context.Context, chatID string, locked bool) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	chatJID, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return err
	}
	if err := client.SetGroupLocked(ctx, chatJID, locked); err != nil {
		return err
	}
	c.refreshGroupInfoLive(ctx, chatJID, "")
	return nil
}
