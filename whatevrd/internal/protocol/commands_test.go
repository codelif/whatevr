package protocol

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

type fakeCommandActions struct {
	mu sync.Mutex

	started []string
	ended   []string
	state   []frontendStateCall

	markReadChat string
	markReadUpTo string
	pinnedChat   string
	pinned       bool
	archivedChat string
	archived     bool
	mutedChat    string
	muted        bool
	muteDuration time.Duration
	typingChat   string
	composing    bool
	requested    bool
	ensuredJID   string

	sendTextChat           string
	sendTextText           string
	sendTextReply          string
	sendTextMentions       []string
	sendMediaChat          string
	sendMediaPath          string
	sendMediaCaption       string
	sendMediaReply         string
	sendMediaMentions      []string
	sendMediaKind          string
	sendMediaViewOnce      bool
	sendMediaFilename      string
	sendMediaBatchChat     string
	sendMediaBatchFiles    []app.MediaBatchFile
	sendMediaBatchReply    string
	sendMediaBatchKind     string
	sendMediaBatchViewOnce bool
	saveMessageID          string
	saveStatusID           string
	saveJID                string
	saveDest               string
	exportChatID           string
	exportDest             string
	viewedStatusID         string
	postedStatusText       string
	postedStatusPath       string
	postedStatusCaption    string
	postedStatusBG         uint32
	postedStatusFont       int32
	downloadedStatusID     string
	repliedStatusID        string
	repliedStatusText      string
	deletedStatusID        string
	keptStatusSender       string
	keptStatusValue        bool
	mutedStatusSender      string
	mutedStatusValue       bool
	sentPollChat           string
	sentPollQuestion       string
	sentPollOptions        []string
	sentPollMulti          bool
	sentContactChat        string
	sentContactName        string
	sentContactPhone       string
	sentLocationChat       string
	sentLocationLat        float64
	sentLocationLong       float64
	createdGroupName       string
	createdGroupMembers    []string
	createdGroupPhoto      string
	leftGroup              string
	groupNameChat          string
	groupName              string
	groupTopicChat         string
	groupTopic             string
	groupPhotoChat         string
	groupPhotoPath         string
	inviteChat             string
	inviteReset            bool
	joinedLink             string
	groupMembersChat       string
	groupMembersAction     string
	groupMembersList       []string
	groupAnnounceChat      string
	groupAnnounce          bool
	groupLockedChat        string
	groupLocked            bool
	rejectedCallChat       string
	backupDest             string
	backupUseKeyring       bool
	backupPassphrase       string
	logsLimit              int
	communityChat          string
	linkedCommunity        string
	linkedGroup            string
	unlinkedCommunity      string
	unlinkedGroup          string
	channelsRefreshed      bool
	followedChannel        string
	followedInvite         string
	unfollowedChannel      string
	mutedChannel           string
	mutedValue             bool
	viewedChannel          string
	viewedServerIDs        []int64
	sendStickerChat        string
	sendStickerKey         string
	sendStickerReply       string
	reactMessage           string
	reactEmoji             string
	editMessage            string
	editText               string
	revokeMessage          string
	deleteMessage          string
	starMessage            string
	starred                bool
	pinMessage             string
	messagePinned          bool
	pinDuration            uint32
	forwardMessage         string
	forwardChats           []string
	downloadMessage        string
	streamMessage          string
	streamUpdate           func(app.MediaStreamUpdate)
	cancelledMessage       string
	playedMessage          string
	rerequestedMessage     string
	fetchJID               string

	joinedInviteMessage string
	rsvpMessage         string
	rsvpResponse        string
	rsvpGuests          int

	privacyCategory    string
	privacyAudience    string
	privacyRead        bool
	prefs              app.AppPreferences
	setPrefs           app.AppPreferences
	profileStatus      string
	blockJID           string
	blocked            bool
	favoriteKey        string
	favoriteMessage    string
	favorite           bool
	downloadSticker    string
	installPackID      string
	installed          bool
	refreshedPacks     bool
	searchChatsQuery   string
	searchChatsLimit   int
	searchMsgQuery     string
	searchMsgChat      string
	searchMsgLimit     int
	searchMsgBefore    string
	searchStickerQuery string
	searchStickerLimit int
	checkPhone         string

	err error

	// pinGate, when non-nil, blocks SetChatPinned until closed — lets tests
	// hold a backgrounded mutation in flight.
	pinGate chan struct{}
	// checkPhoneCtx, when non-nil, makes CheckPhoneOnWhatsApp block until its
	// context is done and then report ctx.Err() — lets tests observe the
	// conn-tied cancellation of backgrounded queries.
	checkPhoneCtx chan error
}

type frontendStateCall struct {
	sessionID    string
	focused      bool
	activeChatID string
}

func (f *fakeCommandActions) FrontendSessionStarted(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, id)
}
func (f *fakeCommandActions) FrontendSessionEnded(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, id)
}
func (f *fakeCommandActions) FrontendSessionStateChanged(id string, focused bool, activeChatID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = append(f.state, frontendStateCall{id, focused, activeChatID})
}
func (f *fakeCommandActions) Reconnect(context.Context) error { return f.err }
func (f *fakeCommandActions) Logout(context.Context) error    { return f.err }
func (f *fakeCommandActions) MarkChatReadUpTo(_ context.Context, chatID, upTo string) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markReadChat, f.markReadUpTo = chatID, upTo
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) MarkChatRead(context.Context, string) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (f *fakeCommandActions) MarkAllChatsRead(context.Context) (int, error) {
	return 3, f.err
}
func (f *fakeCommandActions) SetChatPinned(_ context.Context, chatID string, pinned bool) (appstore.Chat, error) {
	if f.pinGate != nil {
		<-f.pinGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinnedChat, f.pinned = chatID, pinned
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatFavorite(_ context.Context, chatID string, favorite bool) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinnedChat, f.pinned = chatID, favorite
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatArchived(_ context.Context, chatID string, archived bool) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archivedChat, f.archived = chatID, archived
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatMuted(_ context.Context, chatID string, muted bool, d time.Duration) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutedChat, f.muted, f.muteDuration = chatID, muted, d
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatPresence(_ context.Context, chatID string, composing bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typingChat, f.composing = chatID, composing
	return f.err
}
func (f *fakeCommandActions) RequestOlderMessages(_ context.Context, chatID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = true
	return true, f.err
}
func (f *fakeCommandActions) EnsureDirectChat(_ context.Context, jid string) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensuredJID = jid
	return appstore.Chat{ID: "chat-" + jid}, f.err
}
func (f *fakeCommandActions) SendText(_ context.Context, chatID, text, reply string, mentions []string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendTextChat, f.sendTextText, f.sendTextReply, f.sendTextMentions = chatID, text, reply, append([]string(nil), mentions...)
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "text-id", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) ScheduleText(_ context.Context, chatID, text string, sendAt time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendTextChat, f.sendTextText = chatID, text
	return 42, f.err
}
func (f *fakeCommandActions) ListScheduledMessages(_ context.Context, _ string) ([]appstore.ScheduledMessage, error) {
	return nil, nil
}
func (f *fakeCommandActions) CancelScheduledMessage(_ context.Context, _ int64) error { return nil }
func (f *fakeCommandActions) SendMediaWithMentions(_ context.Context, chatID, path, caption, reply string, mentions []string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendMediaChat, f.sendMediaPath, f.sendMediaCaption, f.sendMediaReply, f.sendMediaMentions = chatID, path, caption, reply, append([]string(nil), mentions...)
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "media-id", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendMediaWithOptions(_ context.Context, chatID, path, caption, reply string, mentions []string, opts app.MediaSendOptions) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendMediaChat, f.sendMediaPath, f.sendMediaCaption, f.sendMediaReply, f.sendMediaMentions = chatID, path, caption, reply, append([]string(nil), mentions...)
	f.sendMediaKind, f.sendMediaViewOnce, f.sendMediaFilename = opts.Kind, opts.ViewOnce, opts.Filename
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "media-id", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendMediaBatch(_ context.Context, chatID string, files []app.MediaBatchFile, reply string, opts app.MediaSendOptions) ([]appstore.SavedTextMessage, []app.MediaBatchError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendMediaBatchChat, f.sendMediaBatchFiles, f.sendMediaBatchReply = chatID, files, reply
	f.sendMediaBatchKind, f.sendMediaBatchViewOnce = opts.Kind, opts.ViewOnce
	out := make([]appstore.SavedTextMessage, 0, len(files))
	for i := range files {
		out = append(out, appstore.SavedTextMessage{Message: appstore.Message{ID: "media-batch", ChatID: chatID}})
		_ = i
	}
	return out, nil
}
func (f *fakeCommandActions) SendSticker(_ context.Context, chatID, cacheKey, reply string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendStickerChat, f.sendStickerKey, f.sendStickerReply = chatID, cacheKey, reply
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "sticker-id", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendReaction(_ context.Context, messageID, emoji string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactMessage, f.reactEmoji = messageID, emoji
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) EditMessage(_ context.Context, messageID, text string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editMessage, f.editText = messageID, text
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) RevokeMessage(_ context.Context, messageID string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeMessage = messageID
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) ListMessageEdits(_ context.Context, messageID string) ([]appstore.MessageEdit, error) {
	return []appstore.MessageEdit{{MessageID: messageID, EditedAtMillis: 7, Text: "v1"}}, f.err
}
func (f *fakeCommandActions) DeleteMessageForMe(_ context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteMessage = messageID
	return f.err
}
func (f *fakeCommandActions) SetMessageStarred(_ context.Context, messageID string, starred bool) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starMessage, f.starred = messageID, starred
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) PinMessage(_ context.Context, messageID string, pinned bool, durationSecs uint32) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinMessage, f.messagePinned, f.pinDuration = messageID, pinned, durationSecs
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) ForwardMessage(_ context.Context, messageID string, chatIDs []string) ([]appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forwardMessage, f.forwardChats = messageID, append([]string(nil), chatIDs...)
	out := make([]appstore.SavedTextMessage, 0, len(chatIDs))
	for i, chatID := range chatIDs {
		out = append(out, appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":f" + string(rune('0'+i)), ChatID: chatID}})
	}
	return out, f.err
}
func (f *fakeCommandActions) DownloadMessageMedia(_ context.Context, messageID string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadMessage = messageID
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) StreamMessageMedia(_ context.Context, messageID string, update func(app.MediaStreamUpdate)) (app.MediaStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamMessage = messageID
	f.streamUpdate = update
	return app.MediaStream{StreamID: "stream-" + messageID, URL: "http://127.0.0.1:1/media/" + messageID, Mime: "video/mp4", SizeBytes: 42}, f.err
}
func (f *fakeCommandActions) CancelMessageMediaDownload(_ context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelledMessage = messageID
	return f.err
}
func (f *fakeCommandActions) VotePoll(context.Context, string, []int) error { return nil }

func (f *fakeCommandActions) RespondToEvent(_ context.Context, messageID, response string, guests int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rsvpMessage = messageID
	f.rsvpResponse = response
	f.rsvpGuests = guests
	return f.err
}

func (f *fakeCommandActions) JoinGroupInvite(_ context.Context, messageID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joinedInviteMessage = messageID
	return "120363000000000000@g.us", f.err
}

func (f *fakeCommandActions) MarkMessagePlayed(_ context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playedMessage = messageID
	return f.err
}

func (f *fakeCommandActions) RequestMessageFromPhone(_ context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rerequestedMessage = messageID
	return f.err
}

// waitDownloadMessage polls for the message id media.download reaches the seam
// with; media.download acks immediately and runs the download in a background
// goroutine, so the assertion cannot read the field synchronously.
func (f *fakeCommandActions) waitDownloadMessage(t *testing.T) string {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		f.mu.Lock()
		v := f.downloadMessage
		f.mu.Unlock()
		if v != "" {
			return v
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for media.download to reach the actions seam")
			return ""
		case <-time.After(2 * time.Millisecond):
		}
	}
}
func (f *fakeCommandActions) FetchProfilePicture(_ context.Context, jid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchJID = jid
	return "/cache/avatar.jpg", f.err
}
func (f *fakeCommandActions) SaveMediaToPath(_ context.Context, messageID, statusID, jid, dest string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveMessageID, f.saveStatusID, f.saveJID, f.saveDest = messageID, statusID, jid, dest
	if f.err != nil {
		return "", f.err
	}
	return dest, nil
}
func (f *fakeCommandActions) ExportChat(_ context.Context, chatID, dest string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exportChatID, f.exportDest = chatID, dest
	if f.err != nil {
		return "", f.err
	}
	return dest, nil
}
func (f *fakeCommandActions) MarkStatusViewed(_ context.Context, statusID string) (appstore.StatusUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewedStatusID = statusID
	return appstore.StatusUpdate{ID: statusID}, f.err
}
func (f *fakeCommandActions) PostStatus(_ context.Context, text, path, caption string, background uint32, font int32) (appstore.StatusUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postedStatusText, f.postedStatusPath, f.postedStatusCaption = text, path, caption
	f.postedStatusBG, f.postedStatusFont = background, font
	return appstore.StatusUpdate{ID: "status:9", Text: text}, f.err
}
func (f *fakeCommandActions) DownloadStatusMedia(_ context.Context, statusID string) (appstore.StatusUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadedStatusID = statusID
	return appstore.StatusUpdate{ID: statusID}, f.err
}
func (f *fakeCommandActions) ReplyToStatus(_ context.Context, statusID, text string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repliedStatusID, f.repliedStatusText = statusID, text
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "reply:1"}}, f.err
}
func (f *fakeCommandActions) DeleteStatus(_ context.Context, statusID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedStatusID = statusID
	return f.err
}
func (f *fakeCommandActions) ListStatusViewers(context.Context, string) ([]appstore.StatusViewer, error) {
	return []appstore.StatusViewer{{ViewerJID: "viewer@s.whatsapp.net", ViewedAt: 1}}, f.err
}
func (f *fakeCommandActions) SetStatusKeepSender(_ context.Context, senderID string, kept bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keptStatusSender, f.keptStatusValue = senderID, kept
	return f.err
}
func (f *fakeCommandActions) ListKeptStatusSenders(context.Context) ([]string, error) {
	return []string{"kept@s.whatsapp.net"}, f.err
}
func (f *fakeCommandActions) SetStatusMutedSender(_ context.Context, senderID string, muted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutedStatusSender, f.mutedStatusValue = senderID, muted
	return f.err
}
func (f *fakeCommandActions) ListMutedStatusSenders(context.Context) ([]string, error) {
	return []string{"muted@s.whatsapp.net"}, f.err
}
func (f *fakeCommandActions) SendPoll(_ context.Context, chatID, question string, options []string, multi bool) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentPollChat, f.sentPollQuestion, f.sentPollOptions, f.sentPollMulti = chatID, question, append([]string(nil), options...), multi
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "poll:1", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendContact(_ context.Context, chatID, name, phone string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentContactChat, f.sentContactName, f.sentContactPhone = chatID, name, phone
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "contact:1", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendLocation(_ context.Context, chatID string, lat, long float64, name, address string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentLocationChat, f.sentLocationLat, f.sentLocationLong = chatID, lat, long
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "location:1", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) CreateGroup(_ context.Context, name string, members []string, photo string) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdGroupName, f.createdGroupMembers, f.createdGroupPhoto = name, append([]string(nil), members...), photo
	return appstore.Chat{ID: "g@g.us", Name: name}, f.err
}
func (f *fakeCommandActions) LeaveGroup(_ context.Context, chatID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leftGroup = chatID
	return f.err
}
func (f *fakeCommandActions) SetGroupName(_ context.Context, chatID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupNameChat, f.groupName = chatID, name
	return f.err
}
func (f *fakeCommandActions) SetGroupDescription(_ context.Context, chatID, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupTopicChat, f.groupTopic = chatID, description
	return f.err
}
func (f *fakeCommandActions) SetGroupPhoto(_ context.Context, chatID, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupPhotoChat, f.groupPhotoPath = chatID, path
	return f.err
}
func (f *fakeCommandActions) GetGroupInviteLink(_ context.Context, chatID string, reset bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inviteChat, f.inviteReset = chatID, reset
	return "https://chat.whatsapp.com/abc", f.err
}
func (f *fakeCommandActions) JoinGroupWithLink(_ context.Context, link string) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joinedLink = link
	return appstore.Chat{ID: "joined@g.us"}, f.err
}
func (f *fakeCommandActions) UpdateGroupMembers(_ context.Context, chatID, action string, members []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupMembersChat, f.groupMembersAction, f.groupMembersList = chatID, action, append([]string(nil), members...)
	return f.err
}
func (f *fakeCommandActions) SetGroupAnnounce(_ context.Context, chatID string, announce bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupAnnounceChat, f.groupAnnounce = chatID, announce
	return f.err
}
func (f *fakeCommandActions) SetGroupLocked(_ context.Context, chatID string, locked bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupLockedChat, f.groupLocked = chatID, locked
	return f.err
}
func (f *fakeCommandActions) RejectCall(_ context.Context, chatID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectedCallChat = chatID
	return f.err
}
func (f *fakeCommandActions) ExportBackup(_ context.Context, dest, passphrase string, useKeyring bool) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backupDest, f.backupUseKeyring = dest, useKeyring
	if f.err != nil {
		return "", 0, f.err
	}
	if dest == "" {
		dest = "/tmp/backup.tar.gz"
	}
	return dest, 1234, nil
}
func (f *fakeCommandActions) SetBackupPassphrase(_ context.Context, passphrase string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backupPassphrase = passphrase
	return f.err
}
func (f *fakeCommandActions) RecentLogs(_ context.Context, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logsLimit = limit
	return []string{"l1", "l2"}, f.err
}
func (f *fakeCommandActions) ListCommunitySubgroups(_ context.Context, chatID string) ([]app.CommunityGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.communityChat = chatID
	return []app.CommunityGroup{{ID: "sub@g.us", Name: "Sub"}}, f.err
}
func (f *fakeCommandActions) LinkCommunityGroup(_ context.Context, communityID, groupID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linkedCommunity, f.linkedGroup = communityID, groupID
	return f.err
}
func (f *fakeCommandActions) UnlinkCommunityGroup(_ context.Context, communityID, groupID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlinkedCommunity, f.unlinkedGroup = communityID, groupID
	return f.err
}
func (f *fakeCommandActions) RefreshChannels(_ context.Context) ([]appstore.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channelsRefreshed = true
	return []appstore.Channel{{ID: "chan@newsletter", Name: "Chan"}}, f.err
}
func (f *fakeCommandActions) FollowChannel(_ context.Context, channelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.followedChannel = channelID
	return f.err
}
func (f *fakeCommandActions) FollowChannelByInvite(_ context.Context, invite string) (appstore.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.followedInvite = invite
	return appstore.Channel{ID: "chan@newsletter", Name: "Chan"}, f.err
}
func (f *fakeCommandActions) UnfollowChannel(_ context.Context, channelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unfollowedChannel = channelID
	return f.err
}
func (f *fakeCommandActions) SetChannelMuted(_ context.Context, channelID string, muted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutedChannel, f.mutedValue = channelID, muted
	return f.err
}
func (f *fakeCommandActions) MarkChannelViewed(_ context.Context, channelID string, serverIDs []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewedChannel, f.viewedServerIDs = channelID, serverIDs
	return f.err
}

func (f *fakeCommandActions) ReactToChannelMessage(context.Context, string, int64, string) error {
	return nil
}
func (f *fakeCommandActions) SetPrivacySetting(_ context.Context, category, audience string, readReceipts bool) (app.PrivacySettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privacyCategory, f.privacyAudience, f.privacyRead = category, audience, readReceipts
	return app.PrivacySettings{LastSeen: audience, ReadReceipts: readReceipts}, f.err
}
func (f *fakeCommandActions) UpdateAppPreferences(_ context.Context, apply func(*app.AppPreferences)) (app.AppPreferences, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prefs == (app.AppPreferences{}) {
		f.prefs = app.DefaultAppPreferences()
	}
	prefs := f.prefs
	apply(&prefs)
	f.setPrefs = prefs
	f.prefs = prefs
	return prefs, f.err
}
func (f *fakeCommandActions) SetProfileStatus(_ context.Context, statusText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profileStatus = statusText
	return f.err
}
func (f *fakeCommandActions) UpdateBlocklist(_ context.Context, jid string, block bool) ([]app.BlockedContact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockJID, f.blocked = jid, block
	return []app.BlockedContact{{JID: jid}}, f.err
}
func (f *fakeCommandActions) SetStickerFavorite(_ context.Context, cacheKey, messageID string, favorite bool) (appstore.Sticker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.favoriteKey, f.favoriteMessage, f.favorite = cacheKey, messageID, favorite
	return appstore.Sticker{CacheKey: cacheKey, IsFavorite: favorite}, f.err
}
func (f *fakeCommandActions) DownloadSticker(_ context.Context, cacheKey string) (appstore.Sticker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadSticker = cacheKey
	return appstore.Sticker{CacheKey: cacheKey, LocalPath: "/cache/" + cacheKey + ".webp"}, f.err
}
func (f *fakeCommandActions) SetStickerPackInstalled(_ context.Context, packID string, installed bool) (appstore.StickerPack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installPackID, f.installed = packID, installed
	return appstore.StickerPack{ID: packID, Installed: installed}, f.err
}
func (f *fakeCommandActions) RefreshStickerPacks(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshedPacks = true
	return f.err
}
func (f *fakeCommandActions) SearchChats(_ context.Context, query string, limit int) ([]appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchChatsQuery, f.searchChatsLimit = query, limit
	return []appstore.Chat{{ID: "chat@s.whatsapp.net", Name: "Alice", LastMessage: "hi", LastMessageTime: 10}}, f.err
}
func (f *fakeCommandActions) SearchMessages(_ context.Context, query, chatID string, limit int, beforeMessageID string) ([]appstore.MessageSearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchMsgQuery, f.searchMsgChat, f.searchMsgLimit, f.searchMsgBefore = query, chatID, limit, beforeMessageID
	return []appstore.MessageSearchResult{
		{Message: appstore.Message{ID: "m2", ChatID: "chat@s.whatsapp.net", Text: "hello again", TimestampUnix: 20, SortMS: 20000, Direction: appstore.DirectionIncoming, Status: appstore.StatusDelivered}, ChatName: "Alice"},
		{Message: appstore.Message{ID: "m1", ChatID: "chat@s.whatsapp.net", Text: "hello", TimestampUnix: 10, SortMS: 10000, Direction: appstore.DirectionIncoming, Status: appstore.StatusDelivered}, ChatName: "Alice"},
	}, f.err
}
func (f *fakeCommandActions) SearchStickers(_ context.Context, query string, limit int) ([]appstore.Sticker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchStickerQuery, f.searchStickerLimit = query, limit
	return []appstore.Sticker{
		{CacheKey: "s2", LocalPath: "/cache/s2.webp", MimeType: "image/webp", IsAnimated: true,
			Width: 512, Height: 500, Emojis: "wave hello", AccessibilityText: "Waving",
			PackID: "p1", IsFavorite: true, LastUsed: 20, RecentWeight: 2.5},
		{CacheKey: "s1", MimeType: "image/webp", LastUsed: 10},
	}, f.err
}
func (f *fakeCommandActions) CheckPhoneOnWhatsApp(ctx context.Context, phone string) (app.PhoneCheck, error) {
	if f.checkPhoneCtx != nil {
		<-ctx.Done()
		f.checkPhoneCtx <- ctx.Err()
		return app.PhoneCheck{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkPhone = phone
	return app.PhoneCheck{Registered: true, JID: "123@s.whatsapp.net", DisplayName: "Alice", Phone: "+123"}, f.err
}

func startCommandTestServer(t *testing.T, actions *fakeCommandActions) (string, *Server) {
	t.Helper()
	socketPath, server := startTestServer(t)
	RegisterDaemonCommands(server, actions)
	return socketPath, server
}

func TestC1ChatCommands(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	cases := []struct {
		line string
		want func(t *testing.T)
	}{
		{`{"id":2,"method":"chat.mark_read","params":{"chat_id":"chat@s.whatsapp.net","up_to_message_id":"chat@s.whatsapp.net:m3"}}`, func(t *testing.T) {
			if actions.markReadChat != "chat@s.whatsapp.net" || actions.markReadUpTo != "chat@s.whatsapp.net:m3" {
				t.Fatalf("mark_read call = %q/%q", actions.markReadChat, actions.markReadUpTo)
			}
		}},
		{`{"id":3,"method":"chat.pin","params":{"chat_id":"chat@s.whatsapp.net","pinned":true}}`, func(t *testing.T) {
			if actions.pinnedChat != "chat@s.whatsapp.net" || !actions.pinned {
				t.Fatalf("pin call = %q/%v", actions.pinnedChat, actions.pinned)
			}
		}},
		{`{"id":4,"method":"chat.archive","params":{"chat_id":"chat@s.whatsapp.net","archived":true}}`, func(t *testing.T) {
			if actions.archivedChat != "chat@s.whatsapp.net" || !actions.archived {
				t.Fatalf("archive call = %q/%v", actions.archivedChat, actions.archived)
			}
		}},
		{`{"id":5,"method":"chat.mute","params":{"chat_id":"chat@s.whatsapp.net","muted":true,"duration_secs":60}}`, func(t *testing.T) {
			if actions.mutedChat != "chat@s.whatsapp.net" || !actions.muted || actions.muteDuration != time.Minute {
				t.Fatalf("mute call = %q/%v/%s", actions.mutedChat, actions.muted, actions.muteDuration)
			}
		}},
		{`{"id":6,"method":"chat.typing","params":{"chat_id":"chat@s.whatsapp.net","composing":true}}`, func(t *testing.T) {
			if actions.typingChat != "chat@s.whatsapp.net" || !actions.composing {
				t.Fatalf("typing call = %q/%v", actions.typingChat, actions.composing)
			}
		}},
	}

	for _, tc := range cases {
		c.sendLine(tc.line)
		msg := c.recv()
		if _, ok := msg["result"].(map[string]any); !ok {
			t.Fatalf("command failed: %v", msg)
		}
		tc.want(t)
	}

	c.sendLine(`{"id":7,"method":"chat.request_older","params":{"chat_id":"chat@s.whatsapp.net"}}`)
	msg := c.recv()
	result := msg["result"].(map[string]any)
	if result["requested"] != true || !actions.requested {
		t.Fatalf("request_older result/action = %v/%v", result, actions.requested)
	}

	c.sendLine(`{"id":8,"method":"chat.ensure_direct","params":{"jid":"123@s.whatsapp.net"}}`)
	msg = c.recv()
	result = msg["result"].(map[string]any)
	if result["chat_id"] != "chat-123@s.whatsapp.net" || actions.ensuredJID != "123@s.whatsapp.net" {
		t.Fatalf("ensure_direct result/action = %v/%q", result, actions.ensuredJID)
	}
}

func TestC2SendCommands(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"send.text","params":{"chat_id":"chat@s.whatsapp.net","text":"  hi  ","reply_to":"r1","mentions":[" a@s.whatsapp.net ",""]}}`)
	result := c.recv()["result"].(map[string]any)
	// Whitespace in user-authored text is preserved (only ids/mentions are trimmed).
	if result["message_id"] != "text-id" || actions.sendTextChat != "chat@s.whatsapp.net" || actions.sendTextText != "  hi  " || actions.sendTextReply != "r1" || len(actions.sendTextMentions) != 1 || actions.sendTextMentions[0] != "a@s.whatsapp.net" {
		t.Fatalf("send.text result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":3,"method":"send.media","params":{"chat_id":"chat@s.whatsapp.net","path":"/tmp/p.png","caption":" cap ","reply_to":"r2","mentions":["b@s.whatsapp.net"]}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "media-id" || actions.sendMediaPath != "/tmp/p.png" || actions.sendMediaCaption != " cap " || actions.sendMediaReply != "r2" || len(actions.sendMediaMentions) != 1 {
		t.Fatalf("send.media result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":4,"method":"send.sticker","params":{"chat_id":"chat@s.whatsapp.net","cache_key":"ck","reply_to":"r3"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "sticker-id" || actions.sendStickerKey != "ck" || actions.sendStickerReply != "r3" {
		t.Fatalf("send.sticker result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":41,"method":"send.media_batch","params":{"chat_id":"chat@s.whatsapp.net","reply_to":"r2","kind":"document","files":[{"path":"/tmp/a.pdf","caption":"first"},{"path":"/tmp/b.pdf"}]}}`)
	result = c.recv()["result"].(map[string]any)
	ids, ok := result["message_ids"].([]any)
	if !ok || len(ids) != 2 || actions.sendMediaBatchChat != "chat@s.whatsapp.net" || len(actions.sendMediaBatchFiles) != 2 || actions.sendMediaBatchFiles[0].Caption != "first" || actions.sendMediaBatchKind != "document" {
		t.Fatalf("send.media_batch result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":42,"method":"send.media_batch","params":{"chat_id":"chat@s.whatsapp.net","files":[]}}`)
	if msg := c.recv(); msg["error"] == nil {
		t.Fatalf("send.media_batch without files must fail, got %v", msg)
	}
}

func TestC2MessageAndMediaCommands(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	cases := []struct {
		line string
		want func(t *testing.T)
	}{
		{`{"id":2,"method":"message.react","params":{"message_id":"m1","emoji":""}}`, func(t *testing.T) {
			if actions.reactMessage != "m1" || actions.reactEmoji != "" {
				t.Fatalf("react call = %q/%q", actions.reactMessage, actions.reactEmoji)
			}
		}},
		{`{"id":3,"method":"message.edit","params":{"message_id":"m1","text":" new "}}`, func(t *testing.T) {
			if actions.editMessage != "m1" || actions.editText != " new " {
				t.Fatalf("edit call = %q/%q", actions.editMessage, actions.editText)
			}
		}},
		{`{"id":4,"method":"message.revoke","params":{"message_id":"m1"}}`, func(t *testing.T) {
			if actions.revokeMessage != "m1" {
				t.Fatalf("revoke call = %q", actions.revokeMessage)
			}
		}},
		{`{"id":5,"method":"message.delete","params":{"message_id":"m1"}}`, func(t *testing.T) {
			if actions.deleteMessage != "m1" {
				t.Fatalf("delete call = %q", actions.deleteMessage)
			}
		}},
		{`{"id":6,"method":"message.star","params":{"message_id":"m1","starred":true}}`, func(t *testing.T) {
			if actions.starMessage != "m1" || !actions.starred {
				t.Fatalf("star call = %q/%v", actions.starMessage, actions.starred)
			}
		}},
		{`{"id":7,"method":"message.pin","params":{"message_id":"m1","pinned":true,"duration_secs":60}}`, func(t *testing.T) {
			if actions.pinMessage != "m1" || !actions.messagePinned || actions.pinDuration != 60 {
				t.Fatalf("pin call = %q/%v/%d", actions.pinMessage, actions.messagePinned, actions.pinDuration)
			}
		}},
		{`{"id":8,"method":"media.download","params":{"message_id":"m1"}}`, func(t *testing.T) {
			if got := actions.waitDownloadMessage(t); got != "m1" {
				t.Fatalf("download call = %q", got)
			}
		}},
	}
	for _, tc := range cases {
		c.sendLine(tc.line)
		if _, ok := c.recv()["result"].(map[string]any); !ok {
			t.Fatalf("command failed for %s", tc.line)
		}
		tc.want(t)
	}

	c.sendLine(`{"id":9,"method":"message.forward","params":{"message_id":"m1","chat_ids":["a@s.whatsapp.net","a@s.whatsapp.net","b@s.whatsapp.net"]}}`)
	result := c.recv()["result"].(map[string]any)
	ids := result["message_ids"].([]any)
	if len(ids) != 2 || actions.forwardMessage != "m1" || len(actions.forwardChats) != 2 || actions.forwardChats[1] != "b@s.whatsapp.net" {
		t.Fatalf("forward result/action = %v/%+v", result, actions.forwardChats)
	}

	c.sendLine(`{"id":10,"method":"media.fetch_profile_picture","params":{"jid":" user@s.whatsapp.net "}}`)
	result = c.recv()["result"].(map[string]any)
	if result["path"] != "/cache/avatar.jpg" || actions.fetchJID != "user@s.whatsapp.net" {
		t.Fatalf("fetch profile result/action = %v/%q", result, actions.fetchJID)
	}

	c.sendLine(`{"id":11,"method":"media.save","params":{"message_id":"m1","path":"/tmp/out.jpg"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["path"] != "/tmp/out.jpg" || actions.saveMessageID != "m1" || actions.saveDest != "/tmp/out.jpg" {
		t.Fatalf("media.save result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":12,"method":"media.save","params":{"path":"/tmp/out.jpg"}}`)
	if msg := c.recv(); msg["error"] == nil {
		t.Fatalf("media.save without a selector must fail, got %v", msg)
	} else if errObj, ok := msg["error"].(map[string]any); !ok || errObj["code"] != "invalid_params" {
		t.Fatalf("media.save without a selector must fail invalid_params, got %v", msg)
	}

	c.sendLine(`{"id":121,"method":"chat.export","params":{"chat_id":"chat-1","path":"/tmp/chat-1.txt"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["path"] != "/tmp/chat-1.txt" || actions.exportChatID != "chat-1" || actions.exportDest != "/tmp/chat-1.txt" {
		t.Fatalf("chat.export result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":122,"method":"chat.export","params":{"chat_id":"chat-1"}}`)
	if msg := c.recv(); msg["error"] == nil {
		t.Fatalf("chat.export without a path must fail, got %v", msg)
	} else if errObj, ok := msg["error"].(map[string]any); !ok || errObj["code"] != "invalid_params" {
		t.Fatalf("chat.export without a path must fail invalid_params, got %v", msg)
	}

	c.sendLine(`{"id":13,"method":"status.mark_viewed","params":{"status_id":"status:1"}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.viewedStatusID != "status:1" {
		t.Fatalf("status.mark_viewed action = %q", actions.viewedStatusID)
	}

	c.sendLine(`{"id":14,"method":"status.post","params":{"text":"hello stories"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["status_id"] != "status:9" || actions.postedStatusText != "hello stories" {
		t.Fatalf("status.post result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":141,"method":"status.reply","params":{"status_id":"status:9","text":"nice!"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "reply:1" || actions.repliedStatusText != "nice!" {
		t.Fatalf("status.reply result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":142,"method":"status.delete","params":{"status_id":"status:9"}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.deletedStatusID != "status:9" {
		t.Fatalf("status.delete action = %q", actions.deletedStatusID)
	}

	c.sendLine(`{"id":1421,"method":"status.keep_sender","params":{"sender_id":"k@s.whatsapp.net","kept":true}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.keptStatusSender != "k@s.whatsapp.net" || !actions.keptStatusValue {
		t.Fatalf("status.keep_sender action = %+v", actions)
	}

	c.sendLine(`{"id":14211,"method":"status.mute_sender","params":{"sender_id":"m@s.whatsapp.net","muted":true}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.mutedStatusSender != "m@s.whatsapp.net" || !actions.mutedStatusValue {
		t.Fatalf("status.mute_sender action = %+v", actions)
	}

	c.sendLine(`{"id":1422,"method":"message.edit_history","params":{"message_id":"m1"}}`)
	result = c.recv()["result"].(map[string]any)
	edits, ok := result["edits"].([]any)
	if !ok || len(edits) != 1 || edits[0].(map[string]any)["text"] != "v1" {
		t.Fatalf("message.edit_history result = %v", result)
	}

	c.sendLine(`{"id":143,"method":"send.poll","params":{"chat_id":"c@s.whatsapp.net","question":"dinner?","options":["yes","no"],"multi":true}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "poll:1" || actions.sentPollQuestion != "dinner?" || !actions.sentPollMulti {
		t.Fatalf("send.poll result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":144,"method":"send.poll","params":{"chat_id":"c@s.whatsapp.net","question":"x?","options":["only"]}}`)
	if _, ok := c.recv()["result"].(map[string]any); ok {
		t.Fatal("send.poll with one option must fail")
	}

	c.sendLine(`{"id":146,"method":"send.contact","params":{"chat_id":"c@s.whatsapp.net","name":"Ada","phone":"+123"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "contact:1" || actions.sentContactPhone != "+123" {
		t.Fatalf("send.contact result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":147,"method":"send.location","params":{"chat_id":"c@s.whatsapp.net","lat":12.5,"long":77.5,"name":"Park"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "location:1" || actions.sentLocationLat != 12.5 {
		t.Fatalf("send.location result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":148,"method":"send.location","params":{"chat_id":"c@s.whatsapp.net","lat":200,"long":0}}`)
	if _, ok := c.recv()["result"].(map[string]any); ok {
		t.Fatal("send.location out of range must fail")
	}

	c.sendLine(`{"id":15,"method":"group.create","params":{"name":"team","members":["a@s.whatsapp.net"]}}`)
	result = c.recv()["result"].(map[string]any)
	if result["chat_id"] != "g@g.us" || actions.createdGroupName != "team" || len(actions.createdGroupMembers) != 1 {
		t.Fatalf("group.create result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":16,"method":"group.leave","params":{"chat_id":"g@g.us"}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.leftGroup != "g@g.us" {
		t.Fatalf("group.leave action = %q", actions.leftGroup)
	}

	c.sendLine(`{"id":17,"method":"group.members","params":{"chat_id":"g@g.us","action":"promote","members":["a@s.whatsapp.net"]}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.groupMembersAction != "promote" {
		t.Fatalf("group.members action = %+v", actions)
	}

	c.sendLine(`{"id":18,"method":"group.invite_link","params":{"chat_id":"g@g.us","reset":true}}`)
	result = c.recv()["result"].(map[string]any)
	if result["link"] != "https://chat.whatsapp.com/abc" || !actions.inviteReset {
		t.Fatalf("group.invite_link result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":19,"method":"group.members","params":{"chat_id":"g@g.us","action":"ban","members":["a@s.whatsapp.net"]}}`)
	if _, ok := c.recv()["result"].(map[string]any); ok {
		t.Fatal("group.members with a bad action must fail")
	}

	c.sendLine(`{"id":20,"method":"call.reject","params":{"chat_id":"c@s.whatsapp.net"}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.rejectedCallChat != "c@s.whatsapp.net" {
		t.Fatalf("call.reject action = %q", actions.rejectedCallChat)
	}

	c.sendLine(`{"id":21,"method":"daemon.backup_export","params":{"path":"/tmp/b.tar.gz"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["path"] != "/tmp/b.tar.gz" || actions.backupDest != "/tmp/b.tar.gz" {
		t.Fatalf("backup_export result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":22,"method":"community.subgroups","params":{"chat_id":"com@g.us"}}`)
	result = c.recv()["result"].(map[string]any)
	groups := result["groups"].([]any)
	if len(groups) != 1 || actions.communityChat != "com@g.us" {
		t.Fatalf("community.subgroups result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":23,"method":"community.link","params":{"community_id":"com@g.us","group_id":"sub@g.us"}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || actions.linkedGroup != "sub@g.us" {
		t.Fatalf("community.link action = %+v", actions)
	}

	c.sendLine(`{"id":24,"method":"daemon.logs","params":{"limit":50}}`)
	result = c.recv()["result"].(map[string]any)
	lines := result["lines"].([]any)
	if len(lines) != 2 || actions.logsLimit != 50 {
		t.Fatalf("daemon.logs result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":25,"method":"channels.refresh","params":{}}`)
	result = c.recv()["result"].(map[string]any)
	if result["count"] != float64(1) || !actions.channelsRefreshed {
		t.Fatalf("channels.refresh result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":26,"method":"channel.follow_link","params":{"invite":"abc123"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["channel_id"] != "chan@newsletter" || actions.followedInvite != "abc123" {
		t.Fatalf("channel.follow_link result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":27,"method":"channel.mute","params":{"channel_id":"chan@newsletter","muted":true}}`)
	if _, ok := c.recv()["result"].(map[string]any); !ok || !actions.mutedValue {
		t.Fatalf("channel.mute action = %+v", actions)
	}
	// Joining an invite answers with the chat to open. The frontend has no
	// other way to get there: the group's jid lives inside the message payload,
	// and a card that joined a group and could not open it is half a feature.
	c.sendLine(`{"id":11,"method":"group.join_invite","params":{"message_id":" chat@s.whatsapp.net:m1 "}}`)
	result = c.recv()["result"].(map[string]any)
	if result["chat_id"] != "120363000000000000@g.us" || actions.joinedInviteMessage != "chat@s.whatsapp.net:m1" {
		t.Fatalf("join invite result/action = %v/%q", result, actions.joinedInviteMessage)
	}
}

func TestMediaStreamResponsePrecedesDirectedTerminalUpdate(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	requester := dialTest(t, socketPath)
	requester.hello()
	other := dialTest(t, socketPath)
	other.hello()

	requester.sendLine(`{"id":2,"method":"media.stream","params":{"message_id":"m-video"}}`)
	var notify func(app.MediaStreamUpdate)
	deadline := time.Now().Add(time.Second)
	for notify == nil && time.Now().Before(deadline) {
		actions.mu.Lock()
		notify = actions.streamUpdate
		actions.mu.Unlock()
		if notify == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if notify == nil {
		t.Fatal("media stream callback was not registered")
	}
	// Deliver immediately. The command goroutine must still put its response
	// ahead of this event on the connection queue.
	notify(app.MediaStreamUpdate{
		StreamID:  "stream-m-video",
		MessageID: "m-video",
		State:     "local",
		Path:      "/cache/video.mp4",
	})

	response := requester.recv()
	result, ok := response["result"].(map[string]any)
	if !ok || result["stream_id"] != "stream-m-video" {
		t.Fatalf("first frame = %v, want stream response", response)
	}
	event := requester.recv()
	if event["event"] != "media_stream_update" || event["state"] != "local" || event["path"] != "/cache/video.mp4" {
		t.Fatalf("terminal event = %v", event)
	}

	_ = other.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if line, err := other.r.ReadBytes('\n'); err == nil {
		t.Fatalf("unrelated connection received %q", line)
	}
}

func TestMediaStreamFailedTerminalUpdate(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"media.stream","params":{"message_id":"m-failed"}}`)
	response := c.recv()
	result := response["result"].(map[string]any)
	streamID := result["stream_id"].(string)
	actions.mu.Lock()
	notify := actions.streamUpdate
	actions.mu.Unlock()
	notify(app.MediaStreamUpdate{StreamID: streamID, MessageID: "m-failed", State: "failed", ErrorText: "network down"})
	event := c.recv()
	if event["state"] != "failed" || event["error"] != "network down" {
		t.Fatalf("failed event = %v", event)
	}
}

func TestC3SettingsContactAndStickerCommands(t *testing.T) {
	actions := &fakeCommandActions{prefs: app.AppPreferences{NotificationsEnabled: true, NotificationPreview: true}}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	cases := []struct {
		line string
		want func(t *testing.T)
	}{
		{`{"id":2,"method":"privacy.set","params":{"category":"last_seen","value":"contacts"}}`, func(t *testing.T) {
			if actions.privacyCategory != "last_seen" || actions.privacyAudience != "contacts" {
				t.Fatalf("privacy.set call = %q/%q", actions.privacyCategory, actions.privacyAudience)
			}
		}},
		{`{"id":3,"method":"privacy.set","params":{"category":"read_receipts","value":false}}`, func(t *testing.T) {
			if actions.privacyCategory != "read_receipts" || actions.privacyRead {
				t.Fatalf("read_receipts call = %q/%v", actions.privacyCategory, actions.privacyRead)
			}
		}},
		{`{"id":4,"method":"preferences.set","params":{"notification_preview":false,"auto_download_photos":true}}`, func(t *testing.T) {
			if !actions.setPrefs.NotificationsEnabled || actions.setPrefs.NotificationPreview || !actions.setPrefs.AutoDownloadPhotos {
				t.Fatalf("preferences patch = %+v", actions.setPrefs)
			}
		}},
		{`{"id":5,"method":"self.set_about","params":{"text":" out "}}`, func(t *testing.T) {
			if actions.profileStatus != " out " {
				t.Fatalf("self.set_about text = %q", actions.profileStatus)
			}
		}},
		{`{"id":6,"method":"contact.block","params":{"jid":" user@s.whatsapp.net ","blocked":true}}`, func(t *testing.T) {
			if actions.blockJID != "user@s.whatsapp.net" || !actions.blocked {
				t.Fatalf("contact.block call = %q/%v", actions.blockJID, actions.blocked)
			}
		}},
		{`{"id":7,"method":"sticker.favorite","params":{"message_id":"m1","favorite":true}}`, func(t *testing.T) {
			if actions.favoriteMessage != "m1" || !actions.favorite {
				t.Fatalf("sticker.favorite call = %q/%v", actions.favoriteMessage, actions.favorite)
			}
		}},
		{`{"id":8,"method":"sticker.download","params":{"cache_key":"ck"}}`, func(t *testing.T) {
			if actions.downloadSticker != "ck" {
				t.Fatalf("sticker.download call = %q", actions.downloadSticker)
			}
		}},
		{`{"id":9,"method":"sticker_pack.install","params":{"pack_id":"p1","installed":true}}`, func(t *testing.T) {
			if actions.installPackID != "p1" || !actions.installed {
				t.Fatalf("sticker_pack.install call = %q/%v", actions.installPackID, actions.installed)
			}
		}},
	}

	for _, tc := range cases {
		c.sendLine(tc.line)
		if _, ok := c.recv()["result"].(map[string]any); !ok {
			t.Fatalf("command failed: %s", tc.line)
		}
		tc.want(t)
	}
}

func TestD6StickerQueryAndPackRefresh(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"search.stickers","params":{"query":"  wave  ","limit":2}}`)
	result := c.recv()["result"].(map[string]any)
	stickers := result["stickers"].([]any)
	if len(stickers) != 2 {
		t.Fatalf("search.stickers rows = %v, want 2", stickers)
	}
	first := stickers[0].(map[string]any)
	if first["id"] != "s2" || first["cache_key"] != "s2" || first["local_path"] != "/cache/s2.webp" ||
		first["mime_type"] != "image/webp" || first["is_animated"] != true ||
		first["width"] != float64(512) || first["height"] != float64(500) ||
		first["accessibility_text"] != "Waving" || first["pack_id"] != "p1" ||
		first["is_favorite"] != true || first["last_used_unix"] != float64(20) ||
		first["weight"] != 2.5 {
		t.Fatalf("search.stickers first row = %v", first)
	}
	emojis := first["emojis"].([]any)
	if len(emojis) != 2 || emojis[0] != "wave" || emojis[1] != "hello" {
		t.Fatalf("search.stickers emojis = %v", emojis)
	}
	if second := stickers[1].(map[string]any)["id"]; second != "s1" {
		t.Fatalf("search.stickers order second id = %v, want s1", second)
	}
	if actions.searchStickerQuery != "wave" || actions.searchStickerLimit != 2 {
		t.Fatalf("search.stickers call = %q/%d", actions.searchStickerQuery, actions.searchStickerLimit)
	}

	c.sendLine(`{"id":3,"method":"sticker_packs.refresh","params":{}}`)
	refreshResult := c.recv()["result"].(map[string]any)
	if len(refreshResult) != 0 {
		t.Fatalf("sticker_packs.refresh result = %v, want empty ack", refreshResult)
	}
	actions.mu.Lock()
	refreshed := actions.refreshedPacks
	actions.mu.Unlock()
	if !refreshed {
		t.Fatal("sticker_packs.refresh did not reach actions seam")
	}
}

func TestD6StickerQueryInvalidParams(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	cases := []string{
		`{"id":2,"method":"search.stickers","params":{"limit":1}}`,
		`{"id":3,"method":"search.stickers","params":{"query":"   ","limit":1}}`,
		`{"id":4,"method":"search.stickers","params":{"query":"wave"}}`,
		`{"id":5,"method":"search.stickers","params":{"query":"wave","limit":0}}`,
		`{"id":6,"method":"search.stickers","params":{"query":"wave","limit":-1}}`,
	}
	for _, line := range cases {
		c.sendLine(line)
		if code := errorCode(t, c.recv()); code != CodeInvalidParams {
			t.Fatalf("search.stickers invalid params code = %q, want %q for %s", code, CodeInvalidParams, line)
		}
	}
}

func TestC3Queries(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"search.chats","params":{"query":" ali ","limit":2}}`)
	result := c.recv()["result"].(map[string]any)
	chats := result["chats"].([]any)
	if len(chats) != 1 || chats[0].(map[string]any)["id"] != "chat@s.whatsapp.net" || actions.searchChatsQuery != "ali" || actions.searchChatsLimit != 2 {
		t.Fatalf("search.chats result/action = %v/%q/%d", result, actions.searchChatsQuery, actions.searchChatsLimit)
	}

	c.sendLine(`{"id":3,"method":"search.messages","params":{"query":" hello ","chat_id":"chat@s.whatsapp.net","limit":1,"before_message_id":"m3"}}`)
	result = c.recv()["result"].(map[string]any)
	messages := result["messages"].([]any)
	if len(messages) != 1 || result["has_more"] != true || actions.searchMsgQuery != "hello" || actions.searchMsgLimit != 2 || actions.searchMsgBefore != "m3" {
		t.Fatalf("search.messages result/action = %v/%q/%d/%q", result, actions.searchMsgQuery, actions.searchMsgLimit, actions.searchMsgBefore)
	}
	msg := messages[0].(map[string]any)
	if msg["id"] != "m2" || msg["chat_name"] != "Alice" || msg["fallback"] != "hello again" {
		t.Fatalf("search message row = %v", msg)
	}

	c.sendLine(`{"id":4,"method":"contacts.check_phone","params":{"phone":" +1 23 "}}`)
	result = c.recv()["result"].(map[string]any)
	if result["registered"] != true || result["jid"] != "123@s.whatsapp.net" || actions.checkPhone != "+1 23" {
		t.Fatalf("contacts.check_phone result/action = %v/%q", result, actions.checkPhone)
	}
}

func TestOpenChatRoutesToFocusedProtocolSession(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, server := startCommandTestServer(t, actions)
	unfocused := dialTest(t, socketPath)
	unfocused.hello()
	focused := dialTest(t, socketPath)
	focused.hello()

	if server.OpenChat("chat@s.whatsapp.net") {
		t.Fatal("open_chat delivered before any protocol frontend session existed")
	}

	unfocused.sendLine(`{"id":2,"method":"session.update","params":{"focused":false,"active_chat_id":"old@s.whatsapp.net"}}`)
	if _, ok := unfocused.recv()["result"].(map[string]any); !ok {
		t.Fatal("unfocused session.update failed")
	}
	focused.sendLine(`{"id":2,"method":"session.update","params":{"focused":true,"active_chat_id":"chat@s.whatsapp.net"}}`)
	if _, ok := focused.recv()["result"].(map[string]any); !ok {
		t.Fatal("focused session.update failed")
	}

	if !server.OpenChat(" chat@s.whatsapp.net ") {
		t.Fatal("open_chat was not delivered to a protocol frontend")
	}
	evt := focused.recvEvent()
	if evt["event"] != "open_chat" || evt["chat_id"] != "chat@s.whatsapp.net" {
		t.Fatalf("open_chat event = %v", evt)
	}
	if _, hasSub := evt["sub"]; hasSub {
		t.Fatalf("open_chat must be connection-directed, got sub in %v", evt)
	}
}

func TestC1SessionUpdateStartsAndEndsFrontendSession(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"session.update","params":{"focused":true,"active_chat_id":"chat@s.whatsapp.net"}}`)
	msg := c.recv()
	if _, ok := msg["result"].(map[string]any); !ok {
		t.Fatalf("session.update failed: %v", msg)
	}

	actions.mu.Lock()
	if len(actions.started) != 1 || len(actions.state) != 1 || !actions.state[0].focused || actions.state[0].activeChatID != "chat@s.whatsapp.net" {
		t.Fatalf("session calls before close: started=%v state=%v", actions.started, actions.state)
	}
	sessionID := actions.started[0]
	actions.mu.Unlock()

	_ = c.conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		actions.mu.Lock()
		ended := append([]string(nil), actions.ended...)
		actions.mu.Unlock()
		if len(ended) == 1 && ended[0] == sessionID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	actions.mu.Lock()
	defer actions.mu.Unlock()
	t.Fatalf("session not ended: started=%v ended=%v", actions.started, actions.ended)
}

func TestCommandValidationAndErrors(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"chat.mark_read","params":{"chat_id":"chat@s.whatsapp.net"}}`)
	if got := errorCode(t, c.recv()); got != CodeInvalidParams {
		t.Fatalf("missing up_to error = %s", got)
	}

	c.sendLine(`{"id":21,"method":"chat.mark_all_read","params":{}}`)
	result := c.recv()["result"].(map[string]any)
	if result["marked_chats"] != float64(3) {
		t.Fatalf("chat.mark_all_read result = %v", result)
	}

	actions.err = sql.ErrNoRows
	c.sendLine(`{"id":3,"method":"chat.pin","params":{"chat_id":"missing@s.whatsapp.net","pinned":true}}`)
	if got := errorCode(t, c.recv()); got != CodeNotFound {
		t.Fatalf("sql no rows error = %s", got)
	}

	actions.err = app.NewCommandError(app.CommandErrorNotConnected, "offline")
	c.sendLine(`{"id":4,"method":"daemon.reconnect"}`)
	if got := errorCode(t, c.recv()); got != CodeNotConnected {
		t.Fatalf("unavailable error = %s", got)
	}

	actions.err = nil
	c.sendLine(`{"id":5,"method":"send.media","params":{"chat_id":"chat@s.whatsapp.net"}}`)
	if got := errorCode(t, c.recv()); got != CodeInvalidParams {
		t.Fatalf("missing path error = %s", got)
	}

	c.sendLine(`{"id":6,"method":"message.pin","params":{"message_id":"m1"}}`)
	if got := errorCode(t, c.recv()); got != CodeInvalidParams {
		t.Fatalf("missing pinned error = %s", got)
	}

	actions.err = app.NewCommandError(app.CommandErrorExpired, "the edit window for this message has expired")
	c.sendLine(`{"id":7,"method":"message.edit","params":{"message_id":"m1","text":"new"}}`)
	if got := errorCode(t, c.recv()); got != CodeExpired {
		t.Fatalf("expired error = %s", got)
	}

	actions.err = app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	c.sendLine(`{"id":8,"method":"send.text","params":{"chat_id":"chat@s.whatsapp.net","text":"hi"}}`)
	if got := errorCode(t, c.recv()); got != CodeNotLoggedIn {
		t.Fatalf("not logged in error = %s", got)
	}
}
