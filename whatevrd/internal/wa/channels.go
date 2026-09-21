package wa

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// This file implements the readable slice of WhatsApp Channels
// (newsletters): browse followed channels, read their messages, follow and
// unfollow, mute, and mark viewed. Admin posting and message reactions are
// out of scope for now (noted in PROTOCOL.md).

// RefreshChannels refetches the subscribed-channel directory into the store.
func (c *Client) RefreshChannels(ctx context.Context) ([]appstore.Channel, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return nil, err
	}
	infos, err := client.GetSubscribedNewsletters(ctx)
	if err != nil {
		return nil, err
	}
	channels := make([]appstore.Channel, 0, len(infos))
	for _, info := range infos {
		if info == nil || info.ID.IsEmpty() {
			continue
		}
		channels = append(channels, appstore.Channel{
			ID:          info.ID.String(),
			Name:        strings.TrimSpace(info.ThreadMeta.Name.Text),
			Description: strings.TrimSpace(info.ThreadMeta.Description.Text),
			Followers:   info.ThreadMeta.SubscriberCount,
			Verified:    info.ThreadMeta.VerificationState == types.NewsletterVerificationStateVerified,
			Muted:       info.ViewerMeta != nil && info.ViewerMeta.Mute != types.NewsletterMuteOff,
		})
	}
	if err := c.store.SaveChannels(ctx, channels); err != nil {
		return nil, err
	}
	c.daemon.PublishChannelsChanged()
	return channels, nil
}

func (c *Client) parseChannelJID(chatID string) (types.JID, error) {
	jid, err := types.ParseJID(strings.TrimSpace(chatID))
	if err != nil || jid.User == "" {
		return types.JID{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid channel_id")
	}
	if jid.Server != types.NewsletterServer {
		return types.JID{}, app.NewCommandError(app.CommandErrorInvalidArgument, "not a channel")
	}
	return jid, nil
}

// FollowChannel follows a channel by JID.
func (c *Client) FollowChannel(ctx context.Context, channelID string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	jid, err := c.parseChannelJID(channelID)
	if err != nil {
		return err
	}
	if err := client.FollowNewsletter(ctx, jid); err != nil {
		return err
	}
	_, err = c.RefreshChannels(ctx)
	return err
}

// FollowChannelByInvite follows a channel from an invite code or link.
func (c *Client) FollowChannelByInvite(ctx context.Context, invite string) (appstore.Channel, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return appstore.Channel{}, err
	}
	code := strings.TrimSpace(invite)
	if code == "" {
		return appstore.Channel{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invite is required")
	}
	for _, prefix := range []string{"https://whatsapp.com/channel/", "http://whatsapp.com/channel/", "whatsapp.com/channel/"} {
		code = strings.TrimPrefix(code, prefix)
	}
	info, err := client.GetNewsletterInfoWithInvite(ctx, strings.TrimSpace(code))
	if err != nil {
		return appstore.Channel{}, err
	}
	if err := client.FollowNewsletter(ctx, info.ID); err != nil {
		return appstore.Channel{}, err
	}
	channels, err := c.RefreshChannels(ctx)
	if err != nil {
		return appstore.Channel{}, err
	}
	for _, channel := range channels {
		if channel.ID == info.ID.String() {
			return channel, nil
		}
	}
	return appstore.Channel{ID: info.ID.String(), Name: strings.TrimSpace(info.ThreadMeta.Name.Text)}, nil
}

// UnfollowChannel leaves a channel.
func (c *Client) UnfollowChannel(ctx context.Context, channelID string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	jid, err := c.parseChannelJID(channelID)
	if err != nil {
		return err
	}
	if err := client.UnfollowNewsletter(ctx, jid); err != nil {
		return err
	}
	_, err = c.RefreshChannels(ctx)
	return err
}

// SetChannelMuted mutes or unmutes a channel.
func (c *Client) SetChannelMuted(ctx context.Context, channelID string, muted bool) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	jid, err := c.parseChannelJID(channelID)
	if err != nil {
		return err
	}
	if err := client.NewsletterToggleMute(ctx, jid, muted); err != nil {
		return err
	}
	if err := c.store.SetChannelMuted(ctx, jid.String(), muted); err != nil {
		return err
	}
	c.daemon.PublishChannelsChanged()
	return nil
}

// MarkChannelViewed marks channel messages viewed up to their server IDs.
func (c *Client) MarkChannelViewed(ctx context.Context, channelID string, serverIDs []int64) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	jid, err := c.parseChannelJID(channelID)
	if err != nil {
		return err
	}
	if len(serverIDs) == 0 {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "at least one server id is required")
	}
	ids := make([]types.MessageServerID, 0, len(serverIDs))
	for _, id := range serverIDs {
		ids = append(ids, types.MessageServerID(id))
	}
	return client.NewsletterMarkViewed(ctx, jid, ids)
}

// ReactToChannelMessage adds or removes the current user's reaction.
func (c *Client) ReactToChannelMessage(ctx context.Context, channelID string, serverID int64, emoji string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	jid, err := c.parseChannelJID(channelID)
	if err != nil {
		return err
	}
	return client.NewsletterSendReaction(ctx, jid, types.MessageServerID(serverID), strings.TrimSpace(emoji), "")
}

// GetChannelMessages fetches a channel's recent messages live (never stored).
// before pages older; 0 means latest.
func (c *Client) GetChannelMessages(ctx context.Context, channelID string, count int, before int64) ([]app.ChannelMessage, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return nil, err
	}
	jid, err := c.parseChannelJID(channelID)
	if err != nil {
		return nil, err
	}
	if count <= 0 || count > 100 {
		count = 30
	}
	params := &whatsmeow.GetNewsletterMessagesParams{Count: count}
	if before > 0 {
		params.Before = types.MessageServerID(before)
	}
	msgs, err := client.GetNewsletterMessages(ctx, jid, params)
	if err != nil {
		return nil, err
	}
	out := make([]app.ChannelMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		text := ""
		kind := "text"
		if msg.Message != nil {
			text = strings.TrimSpace(textFromMessage(msg.Message))
			if text == "" {
				kind = channelMessageKind(msg.Message)
				text = channelMessageFallback(msg.Message, kind)
			}
		}
		out = append(out, app.ChannelMessage{
			ServerID:  int64(msg.MessageServerID),
			Timestamp: msg.Timestamp.Unix(),
			Kind:      kind,
			Text:      text,
			Fallback:  text,
			Views:     msg.ViewsCount,
		})
	}
	return out, nil
}

// notifyChannelPost notifies a new channel post like a chat message: fresh,
// globally enabled, channel unmuted, and no frontend viewing it. Channel
// posts are browsed, but unlike statuses they push: followers expect to hear
// about new broadcasts.
func (c *Client) notifyChannelPost(ctx context.Context, evt *waEvents.Message) {
	if c.notifier == nil || evt == nil || evt.Message == nil {
		return
	}
	if !notificationTimestampFresh(evt.Info.Timestamp.Unix(), time.Now()) {
		return
	}
	opts, enabled := c.notificationOptions()
	if !enabled {
		return
	}
	chatID := evt.Info.Chat.String()
	name, muted := c.channelDisplay(ctx, chatID)
	if muted || !c.ShouldNotifyChat(chatID) {
		return
	}
	text := strings.TrimSpace(textFromMessage(evt.Message))
	if text == "" {
		kind := channelMessageKind(evt.Message)
		text = channelMessageFallback(evt.Message, kind)
	}
	c.notifier.NotifyMessage(ctx, app.Message{
		ID:            string(evt.Info.ID),
		ChatID:        chatID,
		Text:          text,
		TimestampUnix: evt.Info.Timestamp.Unix(),
		Direction:     appstore.DirectionIncoming,
	}, app.Chat{ID: chatID, Name: name}, opts)
}

// channelDisplay resolves a channel's directory name and mute flag; unknown
// channels fall back to the bare JID, unmuted.
func (c *Client) channelDisplay(ctx context.Context, chatID string) (string, bool) {
	channels, err := c.store.ListChannels(ctx)
	if err != nil {
		return "", false
	}
	for _, channel := range channels {
		if channel.ID == chatID {
			return channel.Name, channel.Muted
		}
	}
	return "", false
}
func channelMessageKind(msg *waE2E.Message) string {
	switch {
	case msg.GetImageMessage() != nil:
		return "image"
	case msg.GetVideoMessage() != nil:
		return "video"
	case msg.GetAudioMessage() != nil:
		return "audio"
	case msg.GetDocumentMessage() != nil:
		return "document"
	case msg.GetPollCreationMessage() != nil:
		return "poll"
	default:
		return "unsupported"
	}
}

// channelMessageFallback is the one-line rendering for non-text channel
// messages. Channel media is plaintext-hosted and not downloaded, so the
// feed shows the label rather than the bytes.
func channelMessageFallback(msg *waE2E.Message, kind string) string {
	switch kind {
	case "image":
		return "📷 Photo"
	case "video":
		return "🎥 Video"
	case "audio":
		return "🎵 Audio"
	case "document":
		return "📄 Document"
	case "poll":
		return "📊 Poll: " + msg.GetPollCreationMessage().GetName()
	default:
		return "Unsupported message"
	}
}
