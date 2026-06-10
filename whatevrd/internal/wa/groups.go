package wa

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	appstore "whatevrd/internal/store"
)

// groupParticipantsRefreshTTL bounds how long a stored member list is trusted
// for receipt aggregation before a background GetGroupInfo refresh.
const groupParticipantsRefreshTTL = 6 * time.Hour

func (c *Client) handleJoinedGroup(ctx context.Context, evt *events.JoinedGroup) {
	if evt == nil || evt.JID.IsEmpty() {
		return
	}
	chatJID := c.normalizeJIDForChat(ctx, evt.JID)
	if chatJID.Server != types.GroupServer {
		return
	}
	c.storeGroupParticipants(ctx, chatJID, evt.Participants)
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
	c.applyGroupParticipantChanges(ctx, chatJID, evt.Join, evt.Leave)
}

// canonicalParticipantJID reduces a participant JID to the bare phone-number
// form used as the key in group_participants and message_receipts, so LID and
// PN sightings of the same member always collide.
func (c *Client) canonicalParticipantJID(ctx context.Context, jid types.JID) string {
	if jid.IsEmpty() {
		return ""
	}
	jid = c.normalizeJIDForChat(ctx, jid.ToNonAD())
	return jid.ToNonAD().String()
}

// ownParticipantJIDs returns every bare form the local account can appear as
// in a participant list (phone number and LID).
func (c *Client) ownParticipantJIDs() map[string]bool {
	own := make(map[string]bool, 2)
	client := c.currentClient()
	if client == nil {
		return own
	}
	if client.Store.ID != nil {
		own[client.Store.ID.ToNonAD().String()] = true
	}
	if !client.Store.LID.IsEmpty() {
		own[client.Store.LID.ToNonAD().String()] = true
	}
	return own
}

func (c *Client) storeGroupParticipants(ctx context.Context, chatJID types.JID, participants []types.GroupParticipant) {
	if chatJID.IsEmpty() || len(participants) == 0 {
		return
	}
	jids := make([]string, 0, len(participants))
	for _, participant := range participants {
		source := participant.PhoneNumber
		if source.IsEmpty() {
			source = participant.JID
		}
		if canonical := c.canonicalParticipantJID(ctx, source); canonical != "" {
			jids = append(jids, canonical)
		}
	}
	if err := c.store.ReplaceGroupParticipants(ctx, chatJID.String(), jids); err != nil {
		c.log.Warnf("Failed to store participants for %s: %v", chatJID, err)
		return
	}
	c.markGroupParticipantsFresh(chatJID.String())
}

func (c *Client) applyGroupParticipantChanges(ctx context.Context, chatJID types.JID, join, leave []types.JID) {
	chatID := chatJID.String()
	if len(join) > 0 {
		jids := make([]string, 0, len(join))
		for _, jid := range join {
			if canonical := c.canonicalParticipantJID(ctx, jid); canonical != "" {
				jids = append(jids, canonical)
			}
		}
		if err := c.store.AddGroupParticipants(ctx, chatID, jids); err != nil {
			c.log.Warnf("Failed to add participants for %s: %v", chatID, err)
		}
	}
	if len(leave) > 0 {
		jids := make([]string, 0, len(leave))
		for _, jid := range leave {
			if canonical := c.canonicalParticipantJID(ctx, jid); canonical != "" {
				jids = append(jids, canonical)
			}
		}
		if err := c.store.RemoveGroupParticipants(ctx, chatID, jids); err != nil {
			c.log.Warnf("Failed to remove participants for %s: %v", chatID, err)
		}
	}
}

func (c *Client) markGroupParticipantsFresh(chatID string) {
	c.groupParticipantsMu.Lock()
	defer c.groupParticipantsMu.Unlock()
	if c.groupParticipantsFresh == nil {
		c.groupParticipantsFresh = make(map[string]time.Time)
	}
	c.groupParticipantsFresh[chatID] = time.Now()
	delete(c.groupParticipantsInFlight, chatID)
}

// maybeRefreshGroupParticipants kicks off one background GetGroupInfo fetch
// when the stored member list is stale or missing.
func (c *Client) maybeRefreshGroupParticipants(ctx context.Context, chatJID types.JID, haveStored bool) {
	chatID := chatJID.String()

	c.groupParticipantsMu.Lock()
	fresh, ok := c.groupParticipantsFresh[chatID]
	if ok && haveStored && time.Since(fresh) < groupParticipantsRefreshTTL {
		c.groupParticipantsMu.Unlock()
		return
	}
	if c.groupParticipantsInFlight == nil {
		c.groupParticipantsInFlight = make(map[string]bool)
	}
	if c.groupParticipantsInFlight[chatID] {
		c.groupParticipantsMu.Unlock()
		return
	}
	c.groupParticipantsInFlight[chatID] = true
	c.groupParticipantsMu.Unlock()

	go func() {
		defer func() {
			c.groupParticipantsMu.Lock()
			delete(c.groupParticipantsInFlight, chatID)
			c.groupParticipantsMu.Unlock()
		}()
		c.refreshGroupParticipants(context.WithoutCancel(ctx), chatJID)
	}()
}

func (c *Client) refreshGroupParticipants(ctx context.Context, chatJID types.JID) {
	if chatJID.IsEmpty() || chatJID.Server != types.GroupServer {
		return
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}
	info, err := client.GetGroupInfo(ctx, chatJID)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warnf("Failed to fetch group info for participants of %s: %v", chatJID, err)
		}
		return
	}
	if info == nil {
		return
	}
	c.storeGroupParticipants(ctx, chatJID, info.Participants)
}

// groupReceiptParticipants returns the canonical member JIDs the receipt
// aggregate must cover (everyone but us). The bool reports whether the list
// is usable; false means unknown membership, so callers should fall back to
// the any-member status behavior.
func (c *Client) groupReceiptParticipants(ctx context.Context, chatJID types.JID) ([]string, bool) {
	chatID := chatJID.String()
	stored, err := c.store.ListGroupParticipants(ctx, chatID)
	if err != nil {
		c.log.Warnf("Failed to list participants for %s: %v", chatID, err)
		return nil, false
	}

	c.maybeRefreshGroupParticipants(ctx, chatJID, len(stored) > 0)

	if len(stored) == 0 {
		return nil, false
	}

	own := c.ownParticipantJIDs()
	participants := make([]string, 0, len(stored))
	for _, jid := range stored {
		if own[jid] {
			continue
		}
		participants = append(participants, jid)
	}
	if len(participants) == 0 {
		return nil, false
	}
	return participants, true
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
