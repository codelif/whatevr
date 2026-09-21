package wa

import (
	"context"
	"strings"

	"whatevrd/internal/app"
)

// CommunityGroup is one sub-group linked under a community: its JID plus the
// best-effort display name (empty when the group is not in the local store).
type CommunityGroup = app.CommunityGroup

// ListCommunitySubgroups returns the sub-groups linked under a community,
// newest directory first as WhatsApp serves them. Sub-groups are ordinary
// groups otherwise: opening one uses the normal chat flow.
func (c *Client) ListCommunitySubgroups(ctx context.Context, chatID string) ([]CommunityGroup, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return nil, err
	}
	community, err := c.requireGroupJID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	targets, err := client.GetSubGroups(ctx, community)
	if err != nil {
		return nil, err
	}
	groups := make([]CommunityGroup, 0, len(targets))
	for _, target := range targets {
		if target == nil || target.JID.IsEmpty() {
			continue
		}
		id := c.normalizeJIDForChat(ctx, target.JID).String()
		name := strings.TrimSpace(target.GroupName.Name)
		if name == "" {
			if chat, err := c.store.GetChat(ctx, id); err == nil {
				name = chat.Name
			}
		}
		groups = append(groups, CommunityGroup{ID: id, Name: name})
	}
	return groups, nil
}

// LinkCommunityGroup attaches an existing group under a community.
func (c *Client) LinkCommunityGroup(ctx context.Context, communityID, groupID string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	community, err := c.requireGroupJID(ctx, communityID)
	if err != nil {
		return err
	}
	group, err := c.requireGroupJID(ctx, groupID)
	if err != nil {
		return err
	}
	return client.LinkGroup(ctx, community, group)
}

// UnlinkCommunityGroup detaches a sub-group from its community.
func (c *Client) UnlinkCommunityGroup(ctx context.Context, communityID, groupID string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	community, err := c.requireGroupJID(ctx, communityID)
	if err != nil {
		return err
	}
	group, err := c.requireGroupJID(ctx, groupID)
	if err != nil {
		return err
	}
	return client.UnlinkGroup(ctx, community, group)
}
