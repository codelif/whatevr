package wa

import (
	"context"
	"errors"
	"strings"

	"go.mau.fi/whatsmeow/types"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// errNotConnected is returned by the contact/group lookups when there is no
// logged-in WhatsApp client to query (the RPC layer maps it to Unavailable).
var errNotConnected = errors.New("not connected to WhatsApp")

// digitsOnly keeps only ASCII digits, dropping spaces, dashes, parentheses and
// a leading plus so a typed number can be handed to usync.
func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// CheckPhoneOnWhatsApp resolves a typed phone number to a WhatsApp account via
// usync (whatsmeow IsOnWhatsApp), so search can offer to start a chat with a
// number that isn't in any existing chat.
func (c *Client) CheckPhoneOnWhatsApp(ctx context.Context, phone string) (app.PhoneCheck, error) {
	digits := digitsOnly(phone)
	if digits == "" {
		return app.PhoneCheck{}, nil
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return app.PhoneCheck{}, errNotConnected
	}

	formatted := formatPhoneDisplayName(types.NewJID(digits, types.DefaultUserServer))
	if formatted == "" {
		formatted = "+" + digits
	}

	results, err := client.IsOnWhatsApp(ctx, []string{"+" + digits})
	if err != nil {
		return app.PhoneCheck{}, err
	}

	out := app.PhoneCheck{Phone: formatted}
	for _, res := range results {
		if !res.IsIn {
			continue
		}
		out.Registered = true
		out.JID = res.JID.ToNonAD().String()
		if res.VerifiedName != nil && res.VerifiedName.Details != nil {
			if name := strings.TrimSpace(res.VerifiedName.Details.GetVerifiedName()); name != "" {
				out.IsBusiness = true
				out.DisplayName = name
			}
		}
		break
	}
	if out.DisplayName == "" {
		out.DisplayName = formatted
	}
	return out, nil
}

// EnsureDirectChat creates-or-returns the 1:1 chat row for a user JID, resolving
// a display name the same way incoming messages do, and queues its avatar. Used
// by number search and the "Message" buttons in contact info.
func (c *Client) EnsureDirectChat(ctx context.Context, jidStr string) (appstore.Chat, error) {
	jid, err := types.ParseJID(strings.TrimSpace(jidStr))
	if err != nil || jid.User == "" {
		return appstore.Chat{}, errors.New("invalid user jid")
	}
	if jid.Server == types.GroupServer {
		return appstore.Chat{}, errors.New("jid is a group, not a user")
	}

	chatJID := c.normalizeJIDForChat(ctx, jid.ToNonAD())
	chatID := chatJID.String()
	name, source := c.displayNameForChat(ctx, chatJID, false, "", "")

	chat, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, source, false)
	if err != nil {
		return appstore.Chat{}, err
	}

	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID}, avatarPriorityVisible)
	if chat.ID != "" {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return chat, nil
}

// GetContactInfo returns the full contact card for a 1:1 user: saved name, push
// name, verified business name, formatted phone, avatar and status text.
func (c *Client) GetContactInfo(ctx context.Context, jidStr string) (app.ContactInfo, error) {
	jid, err := types.ParseJID(strings.TrimSpace(jidStr))
	if err != nil || jid.User == "" {
		return app.ContactInfo{}, errors.New("invalid user jid")
	}
	if jid.Server == types.GroupServer {
		return app.ContactInfo{}, errors.New("jid is a group, not a user")
	}
	pnJID := c.normalizeJIDForChat(ctx, jid.ToNonAD())

	info := app.ContactInfo{
		JID:         pnJID.String(),
		PhoneNumber: formatPhoneDisplayName(pnJID),
	}

	client := c.currentClient()
	if client != nil && client.Store.Contacts != nil {
		if contact, cErr := client.Store.Contacts.GetContact(ctx, pnJID.ToNonAD()); cErr == nil {
			info.SavedName = firstNonEmpty(contact.FullName, contact.FirstName)
			info.PushName = whatsAppDisplayName(contact.PushName)
			info.BusinessName = strings.TrimSpace(contact.BusinessName)
			info.IsBusiness = info.BusinessName != ""
		}
	}

	// Reuse the sender-display path for the avatar (and as a name fallback);
	// it also queues a background avatar refresh when the cache is cold.
	displayName, avatar := c.participantDisplay(ctx, pnJID.String())
	info.AvatarLocalPath = avatar
	if info.SavedName == "" && info.PushName == "" && info.BusinessName == "" {
		info.PushName = displayName
	}

	// The "about"/status text needs a network round-trip; fetching it inline
	// makes the contact card open slowly. Return the card now from local data
	// and stream the status in afterwards via a ContactInfoUpdated event.
	if client != nil && client.IsLoggedIn() {
		go c.refreshContactStatus(context.WithoutCancel(ctx), pnJID)
	}

	return info, nil
}

// SelfProfile returns the logged-in user's own profile card: their push name,
// formatted phone, avatar and status text. Reuses the contact-info path for the
// avatar/status resolution and prefers the authoritative self push name from the
// device store.
func (c *Client) SelfProfile(ctx context.Context) (app.ContactInfo, error) {
	client := c.currentClient()
	if client == nil || client.Store.ID == nil {
		return app.ContactInfo{}, errors.New("not logged in")
	}
	selfJID := client.Store.ID.ToNonAD()

	info, err := c.GetContactInfo(ctx, selfJID.String())
	if err != nil {
		info = app.ContactInfo{
			JID:         selfJID.String(),
			PhoneNumber: formatPhoneDisplayName(selfJID),
		}
	}
	if name := whatsAppDisplayName(client.Store.PushName); name != "" {
		info.PushName = name
	}
	return info, nil
}

// refreshContactStatus fetches the user's "about"/status text in the background
// and publishes it as a ContactInfoUpdated event once it lands. Best-effort: a
// network hiccup just leaves the card without status text.
func (c *Client) refreshContactStatus(ctx context.Context, pnJID types.JID) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}
	userInfo, err := client.GetUserInfo(ctx, []types.JID{pnJID.ToNonAD()})
	if err != nil {
		return
	}
	ui, ok := userInfo[pnJID.ToNonAD()]
	if !ok {
		return
	}
	status := strings.TrimSpace(ui.Status)
	if status == "" {
		return
	}
	c.daemon.PublishContactInfoUpdated(app.ContactInfo{
		JID:        pnJID.String(),
		StatusText: status,
	})
}

// GetGroupInfo returns the group's subject, description, avatar and resolved
// member list. It prefers a live GetGroupInfo fetch and falls back to the
// stored participant list when the network call fails.
func (c *Client) GetGroupInfo(ctx context.Context, chatID string) (app.GroupInfo, error) {
	chatJID, err := types.ParseJID(strings.TrimSpace(chatID))
	if err != nil || chatJID.Server != types.GroupServer {
		return app.GroupInfo{}, errors.New("invalid group jid")
	}

	out := app.GroupInfo{}

	if chat, cErr := c.store.GetChat(ctx, chatJID.String()); cErr == nil {
		out.Subject = chat.Name
		out.AvatarLocalPath = chat.AvatarLocalPath
	}
	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatJID.String()}, avatarPriorityVisible)

	// Members from stored participants resolve without a network call, so the
	// card opens instantly. The live subject/description/roles/creation time
	// need a GetGroupInfo round-trip; fetch those in the background and stream
	// them in via a GroupInfoUpdated event.
	stored, sErr := c.store.ListGroupParticipants(ctx, chatJID.String())
	if sErr != nil {
		return out, sErr
	}
	out.Members = c.resolveGroupMembers(ctx, storedParticipantSources(stored))

	client := c.currentClient()
	if client != nil && client.IsLoggedIn() {
		go c.refreshGroupInfoLive(context.WithoutCancel(ctx), chatJID, out.AvatarLocalPath)
	}

	return out, nil
}

// groupMemberSource describes one participant before name/avatar resolution.
type groupMemberSource struct {
	canonical string
	isAdmin   bool
	isSuper   bool
	pn        types.JID
}

func storedParticipantSources(stored []string) []groupMemberSource {
	sources := make([]groupMemberSource, 0, len(stored))
	for _, jid := range stored {
		sources = append(sources, groupMemberSource{canonical: jid})
	}
	return sources
}

// resolveGroupMembers turns participant sources into resolved members (display
// name, avatar, formatted phone), de-duplicating by canonical JID.
func (c *Client) resolveGroupMembers(ctx context.Context, sources []groupMemberSource) []app.GroupMember {
	seen := make(map[string]bool, len(sources))
	members := make([]app.GroupMember, 0, len(sources))
	for _, src := range sources {
		if src.canonical == "" || seen[src.canonical] {
			continue
		}
		seen[src.canonical] = true
		name, avatar := c.participantDisplay(ctx, src.canonical)
		member := app.GroupMember{
			JID:             src.canonical,
			DisplayName:     name,
			AvatarLocalPath: avatar,
			IsAdmin:         src.isAdmin || src.isSuper,
			IsSuperAdmin:    src.isSuper,
		}
		if !src.pn.IsEmpty() {
			member.PhoneNumber = formatPhoneDisplayName(src.pn)
		}
		if member.PhoneNumber == "" {
			if parsed, pErr := types.ParseJID(src.canonical); pErr == nil {
				member.PhoneNumber = formatPhoneDisplayName(parsed)
			}
		}
		members = append(members, member)
	}
	return members
}

// refreshGroupInfoLive does the live GetGroupInfo network fetch and publishes
// the enriched card (subject, description, roles, creation time) as a
// GroupInfoUpdated event. Best-effort: on failure the stored card stands.
func (c *Client) refreshGroupInfoLive(ctx context.Context, chatJID types.JID, avatarLocalPath string) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}
	live, err := client.GetGroupInfo(ctx, chatJID)
	if err != nil || live == nil {
		if err != nil && ctx.Err() == nil {
			c.log.Warnf("Failed to fetch group info for %s: %v", chatJID, err)
		}
		return
	}

	sources := make([]groupMemberSource, 0, len(live.Participants))
	for _, p := range live.Participants {
		source := p.PhoneNumber
		if source.IsEmpty() {
			source = p.JID
		}
		sources = append(sources, groupMemberSource{
			canonical: c.canonicalParticipantJID(ctx, source),
			isAdmin:   p.IsAdmin,
			isSuper:   p.IsSuperAdmin,
			pn:        p.PhoneNumber,
		})
	}

	c.daemon.PublishGroupInfoUpdated(chatJID.String(), app.GroupInfo{
		Subject:         strings.TrimSpace(live.Name),
		Description:     strings.TrimSpace(live.Topic),
		AvatarLocalPath: avatarLocalPath,
		CreatedUnix:     live.GroupCreated.Unix(),
		Members:         c.resolveGroupMembers(ctx, sources),
	})
}

// FetchProfilePicture downloads the full-resolution profile picture for a user
// or group and returns its local cache path (empty when there is none).
func (c *Client) FetchProfilePicture(ctx context.Context, jidStr string) (string, error) {
	jid, err := types.ParseJID(strings.TrimSpace(jidStr))
	if err != nil || jid.User == "" {
		return "", errors.New("invalid jid")
	}
	if jid.Server != types.GroupServer {
		jid = c.normalizeJIDForChat(ctx, jid.ToNonAD())
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return "", errNotConnected
	}
	_, localPath, err := c.fetchAndCacheAvatar(ctx, jid, "")
	if err != nil {
		return "", err
	}
	return localPath, nil
}
