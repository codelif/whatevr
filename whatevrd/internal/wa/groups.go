package wa

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	appstore "whatevrd/internal/store"
)

func (c *Client) handleJoinedGroup(ctx context.Context, evt *events.JoinedGroup) {
	if evt == nil || evt.JID.IsEmpty() {
		return
	}
	chatJID := c.normalizeJIDForChat(ctx, evt.JID)
	if chatJID.Server != types.GroupServer {
		return
	}
	if name := strings.TrimSpace(evt.Name); name != "" {
		c.ensureOrUpdateGroupName(ctx, chatJID, name)
		return
	}
	go c.refreshGroupName(context.WithoutCancel(ctx), chatJID)
}

func (c *Client) handleGroupInfoEvent(ctx context.Context, evt *events.GroupInfo) {
	if evt == nil || evt.JID.IsEmpty() {
		return
	}
	chatJID := c.normalizeJIDForChat(ctx, evt.JID)
	if chatJID.Server != types.GroupServer {
		return
	}
	if evt.Name != nil && strings.TrimSpace(evt.Name.Name) != "" {
		c.ensureOrUpdateGroupName(ctx, chatJID, evt.Name.Name)
	}
}

func (c *Client) refreshRawGroupNameForChat(ctx context.Context, chat appstore.Chat) {
	if !chat.IsGroup || chat.ID == "" || chat.NameSource != appstore.ChatNameSourceRaw {
		return
	}
	jid, err := types.ParseJID(chat.ID)
	if err != nil || jid.Server != types.GroupServer {
		return
	}
	go c.refreshGroupName(context.WithoutCancel(ctx), jid)
}

func (c *Client) startUnresolvedGroupNameBackfill(ctx context.Context) {
	go c.backfillUnresolvedGroupNames(context.WithoutCancel(ctx))
}

func (c *Client) backfillUnresolvedGroupNames(ctx context.Context) {
	chatIDs, err := c.store.ListRawGroupChatIDs(ctx, 200)
	if err != nil {
		c.log.Warnf("Failed to list unresolved group chat names: %v", err)
		return
	}
	for _, chatID := range chatIDs {
		if ctx.Err() != nil {
			return
		}
		jid, err := types.ParseJID(chatID)
		if err != nil || jid.Server != types.GroupServer {
			continue
		}
		c.refreshGroupName(ctx, jid)
	}
}

func (c *Client) refreshGroupName(ctx context.Context, jid types.JID) {
	if jid.IsEmpty() || jid.Server != types.GroupServer {
		return
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}
	info, err := client.GetGroupInfo(ctx, jid)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warnf("Failed to fetch group info for %s: %v", jid, err)
		}
		return
	}
	if info == nil || strings.TrimSpace(info.Name) == "" {
		return
	}
	c.ensureOrUpdateGroupName(ctx, jid, info.Name)
}

func (c *Client) ensureOrUpdateGroupName(ctx context.Context, jid types.JID, name string) {
	name = strings.TrimSpace(name)
	if jid.IsEmpty() || jid.Server != types.GroupServer || name == "" {
		return
	}
	chatID := jid.String()
	chat, changed, err := c.store.UpdateChatNameWithSource(ctx, chatID, name, appstore.ChatNameSourceGroup)
	if err != nil {
		c.log.Warnf("Failed to update group chat name for %s: %v", chatID, err)
		return
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
		return
	}
	chat, err = c.store.EnsureChatWithNameSource(ctx, chatID, name, appstore.ChatNameSourceGroup, true)
	if err != nil {
		c.log.Warnf("Failed to ensure group chat %s: %v", chatID, err)
		return
	}
	if chat.ID != "" {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
}
