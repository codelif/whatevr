package protocol

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

const (
	maxCommandTextRunes      = 65536
	maxCommandCaptionRunes   = 1024
	maxCommandForwardTargets = 5
	// maxCommandDurationSecs bounds any seconds→time.Duration conversion so the
	// nanosecond multiply cannot overflow int64 (and rejects absurd values well
	// before that): ~100 years is far beyond any real mute/pin horizon.
	maxCommandDurationSecs = 100 * 365 * 24 * 60 * 60
	// groupJIDSuffix is the WhatsApp server suffix for group jids; contact-only
	// commands reject any jid carrying it (the protocol layer works with string
	// jids and must not import wa's jid types).
	groupJIDSuffix = "@g.us"

	// netCommandTimeout bounds a backgrounded command's WhatsApp round trip.
	// Without it a wedged upstream call pins its goroutine until whatsmeow's
	// keepalive tears the socket down; 60s comfortably exceeds normal round
	// trips and the app-state conflict-resync path.
	netCommandTimeout = 60 * time.Second
)

// ctxHandlerFunc is a command handler whose upstream work is bounded by the
// context backgroundNet supplies.
type ctxHandlerFunc func(ctx context.Context, c *conn, req request) (any, *Error)

// backgroundNet runs a network-bound command handler off the connection's
// read/dispatch loop, so one slow WhatsApp round trip cannot stall every
// subsequent request on the connection (PROTOCOL.md: responses may arrive in
// any order relative to other requests; subscribe/extend keep their ordering
// because they stay synchronous). Mutations detach from the connection
// lifetime — a fire-and-forget client may send and exit before the ack — while
// queries (tieToConn) derive from the connection context: a result nobody can
// receive is not worth finishing. req's fields are json.RawMessage copies, so
// capturing req here does not alias the read loop's scanner buffer, and a
// response pushed after close is a harmless drop.
func backgroundNet(h ctxHandlerFunc, tieToConn bool) handlerFunc {
	return func(c *conn, req request) (any, *Error) {
		go func() {
			parent := context.Background()
			if tieToConn {
				parent = c.ctx
			}
			ctx, cancel := context.WithTimeout(parent, netCommandTimeout)
			defer cancel()
			result, herr := h(ctx, c, req)
			if herr != nil {
				c.respondError(req.ID, herr, false)
				return
			}
			if result == nil {
				result = map[string]any{}
			}
			c.respondResult(req.ID, result)
		}()
		return responded{}, nil
	}
}

// CommandActions is the daemon/WA seam used by protocol commands. *wa.Client
// implements it; tests and the conformance fixture can provide a small fake.
type CommandActions interface {
	FrontendSessionStarted(string)
	FrontendSessionEnded(string)
	FrontendSessionStateChanged(string, bool, string)

	Reconnect(context.Context) error
	Logout(context.Context) error

	MarkChatReadUpTo(context.Context, string, string) (appstore.Chat, error)
	MarkChatRead(context.Context, string) (appstore.Chat, error)
	MarkAllChatsRead(context.Context) (int, error)
	ExportChat(context.Context, string, string) (string, error)
	SetChatPinned(context.Context, string, bool) (appstore.Chat, error)
	SetChatFavorite(context.Context, string, bool) (appstore.Chat, error)
	SetChatArchived(context.Context, string, bool) (appstore.Chat, error)
	SetChatMuted(context.Context, string, bool, time.Duration) (appstore.Chat, error)
	SetChatPresence(context.Context, string, bool) error
	RequestOlderMessages(context.Context, string) (bool, error)
	EnsureDirectChat(context.Context, string) (appstore.Chat, error)

	SendText(context.Context, string, string, string, []string) (appstore.SavedTextMessage, error)
	ScheduleText(context.Context, string, string, time.Time) (int64, error)
	ListScheduledMessages(context.Context, string) ([]appstore.ScheduledMessage, error)
	CancelScheduledMessage(context.Context, int64) error
	SendMediaWithMentions(context.Context, string, string, string, string, []string) (appstore.SavedTextMessage, error)
	SendMediaWithOptions(context.Context, string, string, string, string, []string, app.MediaSendOptions) (appstore.SavedTextMessage, error)
	SendPoll(context.Context, string, string, []string, bool) (appstore.SavedTextMessage, error)
	SendContact(context.Context, string, string, string) (appstore.SavedTextMessage, error)
	SendLocation(context.Context, string, float64, float64, string, string) (appstore.SavedTextMessage, error)
	SendMediaBatch(context.Context, string, []app.MediaBatchFile, string, app.MediaSendOptions) ([]appstore.SavedTextMessage, []app.MediaBatchError)
	SendSticker(context.Context, string, string, string) (appstore.SavedTextMessage, error)
	SendReaction(context.Context, string, string) (appstore.Message, error)
	EditMessage(context.Context, string, string) (appstore.Message, error)
	RevokeMessage(context.Context, string) (appstore.Message, error)
	ListMessageEdits(context.Context, string) ([]appstore.MessageEdit, error)
	DeleteMessageForMe(context.Context, string) error
	SetMessageStarred(context.Context, string, bool) (appstore.Message, error)
	PinMessage(context.Context, string, bool, uint32) (appstore.Message, error)
	ForwardMessage(context.Context, string, []string) ([]appstore.SavedTextMessage, error)
	DownloadMessageMedia(context.Context, string) (appstore.Message, error)
	StreamMessageMedia(context.Context, string, func(app.MediaStreamUpdate)) (app.MediaStream, error)
	CancelMessageMediaDownload(context.Context, string) error
	MarkMessagePlayed(context.Context, string) error
	RequestMessageFromPhone(context.Context, string) error
	VotePoll(context.Context, string, []int) error
	JoinGroupInvite(context.Context, string) (string, error)
	RespondToEvent(context.Context, string, string, int) error
	FetchProfilePicture(context.Context, string) (string, error)
	SaveMediaToPath(context.Context, string, string, string, string) (string, error)
	MarkStatusViewed(context.Context, string) (appstore.StatusUpdate, error)
	PostStatus(context.Context, string, string, string, uint32, int32) (appstore.StatusUpdate, error)
	DownloadStatusMedia(context.Context, string) (appstore.StatusUpdate, error)
	ReplyToStatus(context.Context, string, string) (appstore.SavedTextMessage, error)
	DeleteStatus(context.Context, string) error
	ListStatusViewers(context.Context, string) ([]appstore.StatusViewer, error)
	SetStatusKeepSender(context.Context, string, bool) error
	ListKeptStatusSenders(context.Context) ([]string, error)
	SetStatusMutedSender(context.Context, string, bool) error
	ListMutedStatusSenders(context.Context) ([]string, error)

	CreateGroup(context.Context, string, []string, string) (appstore.Chat, error)
	LeaveGroup(context.Context, string) error
	SetGroupName(context.Context, string, string) error
	SetGroupDescription(context.Context, string, string) error
	SetGroupPhoto(context.Context, string, string) error
	GetGroupInviteLink(context.Context, string, bool) (string, error)
	JoinGroupWithLink(context.Context, string) (appstore.Chat, error)
	UpdateGroupMembers(context.Context, string, string, []string) error
	SetGroupAnnounce(context.Context, string, bool) error
	SetGroupLocked(context.Context, string, bool) error

	RejectCall(context.Context, string) error

	ExportBackup(context.Context, string, string, bool) (string, int64, error)
	SetBackupPassphrase(context.Context, string) error
	RecentLogs(context.Context, int) ([]string, error)
	ListCommunitySubgroups(context.Context, string) ([]app.CommunityGroup, error)
	LinkCommunityGroup(context.Context, string, string) error
	UnlinkCommunityGroup(context.Context, string, string) error

	RefreshChannels(context.Context) ([]appstore.Channel, error)
	FollowChannel(context.Context, string) error
	FollowChannelByInvite(context.Context, string) (appstore.Channel, error)
	UnfollowChannel(context.Context, string) error
	SetChannelMuted(context.Context, string, bool) error
	MarkChannelViewed(context.Context, string, []int64) error
	ReactToChannelMessage(context.Context, string, int64, string) error

	SetPrivacySetting(context.Context, string, string, bool) (app.PrivacySettings, error)
	UpdateAppPreferences(context.Context, func(*app.AppPreferences)) (app.AppPreferences, error)
	SetProfileStatus(context.Context, string) error
	UpdateBlocklist(context.Context, string, bool) ([]app.BlockedContact, error)
	SetStickerFavorite(context.Context, string, string, bool) (appstore.Sticker, error)
	DownloadSticker(context.Context, string) (appstore.Sticker, error)
	SetStickerPackInstalled(context.Context, string, bool) (appstore.StickerPack, error)
	RefreshStickerPacks(context.Context) error
	SearchChats(context.Context, string, int) ([]appstore.Chat, error)
	SearchMessages(context.Context, string, string, int, string) ([]appstore.MessageSearchResult, error)
	SearchStickers(context.Context, string, int) ([]appstore.Sticker, error)
	CheckPhoneOnWhatsApp(context.Context, string) (app.PhoneCheck, error)
}

// RegisterDaemonCommands registers the command surface from PROTOCOL.md.
func RegisterDaemonCommands(s *Server, actions CommandActions) {
	s.commandActions = actions
	cmd := commandHandlers{actions: actions, server: s}
	// Local-only commands (store enqueue, session state, transient DB queries)
	// stay synchronous on the dispatch loop; anything that performs a WhatsApp
	// round trip is backgrounded via backgroundNet so it cannot stall the
	// connection. Text/media sends only enqueue local work and stay synchronous;
	// sticker send may first fetch a missing sticker file, so it is backgrounded.
	s.RegisterCommand("session.update", cmd.sessionUpdate)
	s.RegisterCommand("daemon.reconnect", cmd.daemonReconnect)
	s.RegisterCommand("daemon.shutdown", cmd.daemonShutdown)
	s.RegisterCommand("account.logout", backgroundNet(cmd.accountLogout, false))
	s.RegisterCommand("chat.mark_read", backgroundNet(cmd.chatMarkRead, false))
	s.RegisterCommand("chat.mark_all_read", backgroundNet(cmd.chatMarkAllRead, false))
	s.RegisterCommand("chat.pin", backgroundNet(cmd.chatPin, false))
	s.RegisterCommand("chat.favorite", backgroundNet(cmd.chatFavorite, false))
	s.RegisterCommand("chat.archive", backgroundNet(cmd.chatArchive, false))
	s.RegisterCommand("chat.mute", backgroundNet(cmd.chatMute, false))
	s.RegisterCommand("chat_folder.create", backgroundNet(cmd.folderCreate, false))
	s.RegisterCommand("chat_folder.rename", backgroundNet(cmd.folderRename, false))
	s.RegisterCommand("chat_folder.delete", backgroundNet(cmd.folderDelete, false))
	s.RegisterCommand("chat_folder.set_chat", backgroundNet(cmd.folderSetChat, false))
	s.RegisterCommand("chat.typing", backgroundNet(cmd.chatTyping, false))
	s.RegisterCommand("chat.request_older", backgroundNet(cmd.chatRequestOlder, false))
	s.RegisterCommand("chat.ensure_direct", cmd.chatEnsureDirect)
	s.RegisterCommand("chat.export", backgroundNet(cmd.chatExport, false))
	s.RegisterCommand("send.text", cmd.sendText)
	s.RegisterCommand("schedule.text", cmd.scheduleText)
	s.RegisterCommand("schedule.list", cmd.scheduleList)
	s.RegisterCommand("schedule.cancel", cmd.scheduleCancel)
	s.RegisterCommand("send.media", cmd.sendMedia)
	s.RegisterCommand("send.media_batch", cmd.sendMediaBatch)
	s.RegisterCommand("send.sticker", backgroundNet(cmd.sendSticker, false))
	s.RegisterCommand("send.poll", backgroundNet(cmd.sendPoll, false))
	s.RegisterCommand("send.contact", backgroundNet(cmd.sendContact, false))
	s.RegisterCommand("send.location", backgroundNet(cmd.sendLocation, false))
	s.RegisterCommand("message.react", backgroundNet(cmd.messageReact, false))
	s.RegisterCommand("message.edit", backgroundNet(cmd.messageEdit, false))
	s.RegisterCommand("message.edit_history", cmd.messageEditHistory)
	s.RegisterCommand("message.revoke", backgroundNet(cmd.messageRevoke, false))
	s.RegisterCommand("message.delete", cmd.messageDelete)
	s.RegisterCommand("message.star", backgroundNet(cmd.messageStar, false))
	s.RegisterCommand("message.pin", backgroundNet(cmd.messagePin, false))
	s.RegisterCommand("message.forward", cmd.messageForward)
	s.RegisterCommand("message.mark_played", backgroundNet(cmd.messageMarkPlayed, false))
	s.RegisterCommand("message.request_from_phone", backgroundNet(cmd.messageRequestFromPhone, false))
	s.RegisterCommand("poll.vote", backgroundNet(cmd.pollVote, false))
	s.RegisterCommand("group.join_invite", backgroundNet(cmd.groupJoinInvite, false))
	s.RegisterCommand("event.rsvp", backgroundNet(cmd.eventRSVP, false))
	s.RegisterCommand("media.download", cmd.mediaDownload)
	s.RegisterCommand("media.stream", cmd.mediaStreamCommand)
	s.RegisterCommand("media.cancel_download", backgroundNet(cmd.mediaCancelDownload, false))
	s.RegisterCommand("media.fetch_profile_picture", backgroundNet(cmd.mediaFetchProfilePicture, true))
	s.RegisterCommand("media.save", backgroundNet(cmd.mediaSave, false))
	s.RegisterCommand("status.mark_viewed", backgroundNet(cmd.statusMarkViewed, false))
	s.RegisterCommand("status.post", backgroundNet(cmd.statusPost, false))
	s.RegisterCommand("status.download", cmd.statusDownload)
	s.RegisterCommand("status.reply", backgroundNet(cmd.statusReply, false))
	s.RegisterCommand("status.keep_sender", cmd.statusKeepSender)
	s.RegisterCommand("status.mute_sender", cmd.statusMuteSender)
	s.RegisterCommand("status.delete", backgroundNet(cmd.statusDelete, false))
	s.RegisterCommand("group.create", backgroundNet(cmd.groupCreate, false))
	s.RegisterCommand("group.leave", backgroundNet(cmd.groupLeave, false))
	s.RegisterCommand("group.set_name", backgroundNet(cmd.groupSetName, false))
	s.RegisterCommand("group.set_topic", backgroundNet(cmd.groupSetTopic, false))
	s.RegisterCommand("group.set_photo", backgroundNet(cmd.groupSetPhoto, false))
	s.RegisterCommand("group.invite_link", backgroundNet(cmd.groupInviteLink, true))
	s.RegisterCommand("group.join_link", backgroundNet(cmd.groupJoinLink, false))
	s.RegisterCommand("group.members", backgroundNet(cmd.groupMembers, false))
	s.RegisterCommand("group.set_announce", backgroundNet(cmd.groupSetAnnounce, false))
	s.RegisterCommand("group.set_locked", backgroundNet(cmd.groupSetLocked, false))
	s.RegisterCommand("call.reject", backgroundNet(cmd.callReject, false))
	s.RegisterCommand("daemon.backup_export", backgroundNet(cmd.backupExport, false))
	s.RegisterCommand("daemon.backup_set_passphrase", backgroundNet(cmd.backupSetPassphrase, false))
	s.RegisterCommand("daemon.logs", cmd.daemonLogs)
	s.RegisterCommand("community.subgroups", backgroundNet(cmd.communitySubgroups, true))
	s.RegisterCommand("community.link", backgroundNet(cmd.communityLink, false))
	s.RegisterCommand("community.unlink", backgroundNet(cmd.communityUnlink, false))
	s.RegisterCommand("channels.refresh", backgroundNet(cmd.channelsRefresh, false))
	s.RegisterCommand("channel.follow", backgroundNet(cmd.channelFollow, false))
	s.RegisterCommand("channel.follow_link", backgroundNet(cmd.channelFollowLink, false))
	s.RegisterCommand("channel.unfollow", backgroundNet(cmd.channelUnfollow, false))
	s.RegisterCommand("channel.mute", backgroundNet(cmd.channelMute, false))
	s.RegisterCommand("channel.mark_viewed", backgroundNet(cmd.channelMarkViewed, false))
	s.RegisterCommand("channel.react", backgroundNet(cmd.channelReact, false))
	// Phase C3 settings/contact/sticker commands and transient queries.
	s.RegisterCommand("privacy.set", backgroundNet(cmd.privacySet, false))
	s.RegisterCommand("preferences.set", cmd.preferencesSet)
	s.RegisterCommand("self.set_about", backgroundNet(cmd.selfSetAbout, false))
	s.RegisterCommand("contact.block", backgroundNet(cmd.contactBlock, false))
	s.RegisterCommand("sticker.favorite", backgroundNet(cmd.stickerFavorite, false))
	s.RegisterCommand("sticker.download", backgroundNet(cmd.stickerDownload, false))
	s.RegisterCommand("sticker_pack.install", cmd.stickerPackInstall)
	s.RegisterCommand("sticker_packs.refresh", backgroundNet(cmd.stickerPacksRefresh, false))
	s.RegisterCommand("search.chats", cmd.searchChats)
	s.RegisterCommand("search.messages", cmd.searchMessages)
	s.RegisterCommand("search.stickers", cmd.searchStickers)
	s.RegisterCommand("contacts.check_phone", backgroundNet(cmd.contactsCheckPhone, true))
}

// RegisterCommand makes a command request method available. Registration is
// mutex-guarded, so it is safe even if the server is already accepting
// connections; production wiring still registers everything before Serve so no
// client can observe a half-registered surface.
func (s *Server) RegisterCommand(name string, h handlerFunc) {
	s.handlerMu.Lock()
	defer s.handlerMu.Unlock()
	s.handlers[name] = h
}

// handler looks up a method's handler under the registry lock.
func (s *Server) handler(name string) (handlerFunc, bool) {
	s.handlerMu.RLock()
	defer s.handlerMu.RUnlock()
	h, ok := s.handlers[name]
	return h, ok
}

type commandHandlers struct {
	actions CommandActions
	server  *Server
}

func (h commandHandlers) requireActions() *Error {
	if h.actions == nil {
		return errorf(CodeInternal, "command actions are not available")
	}
	return nil
}

func (c *conn) ensureFrontendSession(actions CommandActions) {
	c.sessionMu.Lock()
	if c.sessionActive {
		c.sessionMu.Unlock()
		return
	}
	if c.sessionID == "" {
		c.sessionID = "protocol-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	id := c.sessionID
	c.sessionActive = true
	c.sessionUpdatedAt = time.Now()
	c.sessionMu.Unlock()
	actions.FrontendSessionStarted(id)
}

func decodeParams(raw json.RawMessage, out any) *Error {
	if len(raw) == 0 {
		return errorf(CodeInvalidParams, "params are required")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errorf(CodeInvalidParams, "malformed params")
	}
	return nil
}

func rejectNonEmptyParams(raw json.RawMessage) *Error {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return errorf(CodeInvalidParams, "malformed params")
	}
	if len(obj) != 0 {
		return errorf(CodeInvalidParams, "params must be empty")
	}
	return nil
}

func trimStringSlice(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func uniqueTrimmedStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
