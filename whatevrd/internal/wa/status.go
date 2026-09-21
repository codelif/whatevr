package wa

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// isStatusBroadcast reports whether an event is a contact status (story)
// rather than a chat message. Statuses arrive as ordinary events.Message from
// status@broadcast; ingesting them as chat messages would materialize a bogus
// "status" chat row, so handleMessage routes them here instead.
func isStatusBroadcast(evt *events.Message) bool {
	return evt != nil && evt.Info.Chat == types.StatusBroadcastJID
}

// DownloadStatusMedia fetches a status payload into the cache (no-op when
// already there) and publishes the change. Text statuses fail rejected.
func (c *Client) DownloadStatusMedia(ctx context.Context, statusID string) (appstore.StatusUpdate, error) {
	statusID = strings.TrimSpace(statusID)
	if statusID == "" {
		return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorInvalidArgument, "status_id is required")
	}
	status, err := c.store.GetStatusUpdate(ctx, statusID)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	if strings.TrimSpace(status.MediaLocalPath) != "" {
		if _, err := os.Stat(status.MediaLocalPath); err == nil {
			return status, nil
		}
	}
	return c.downloadStatusMedia(ctx, status)
}

// statusImageDimensions decodes image dimensions for own image posts so the
// status feed carries the same rendering facts as chat media. Non-image kinds
// stay zero (video dims need a container parser we don't have).
func statusImageDimensions(mediaKind string, data []byte) (width, height int32) {
	if mediaKind == appstore.MediaKindImage {
		return decodedImageDimensions(data)
	}
	return 0, 0
}

// defaultStatusBackground is the text-status backdrop when the caller picks
// none: WhatsApp's dark outgoing green.
const defaultStatusBackground = 0xFF075E54

// statusFont clamps a caller font id to the WhatsApp FontType enum, defaulting
// to SYSTEM (0) for anything unknown.
func statusFont(font int32) *waE2E.ExtendedTextMessage_FontType {
	switch waE2E.ExtendedTextMessage_FontType(font) {
	case waE2E.ExtendedTextMessage_SYSTEM,
		waE2E.ExtendedTextMessage_SYSTEM_TEXT,
		waE2E.ExtendedTextMessage_FB_SCRIPT,
		waE2E.ExtendedTextMessage_SYSTEM_BOLD,
		waE2E.ExtendedTextMessage_MORNINGBREEZE_REGULAR,
		waE2E.ExtendedTextMessage_CALISTOGA_REGULAR,
		waE2E.ExtendedTextMessage_EXO2_EXTRABOLD,
		waE2E.ExtendedTextMessage_COURIERPRIME_BOLD:
		f := waE2E.ExtendedTextMessage_FontType(font)
		return &f
	default:
		f := waE2E.ExtendedTextMessage_SYSTEM
		return &f
	}
}

// recordStatusViewers files viewed receipts on status@broadcast as status
// viewers. Only receipts for our own statuses (sender "me" rows) are kept;
// anything else is someone else's business.
func (c *Client) recordStatusViewers(ctx context.Context, evt *events.Receipt) {
	if evt == nil || len(evt.MessageIDs) == 0 {
		return
	}
	viewer := c.canonicalParticipantJID(ctx, evt.Sender)
	if viewer == "" {
		return
	}
	own := c.ownParticipantJIDs()
	if own[viewer] {
		return
	}
	viewedAt := evt.Timestamp
	if viewedAt.IsZero() {
		viewedAt = time.Now()
	}
	changed := false
	for _, messageID := range evt.MessageIDs {
		statusID := "status:" + string(messageID)
		status, err := c.store.GetStatusUpdate(ctx, statusID)
		if err != nil || strings.TrimSpace(status.SenderID) != "me" {
			continue
		}
		if err := c.store.RecordStatusViewer(ctx, statusID, viewer, viewedAt); err != nil {
			c.log.Warnf("Failed to record status viewer for %s: %v", statusID, err)
			continue
		}
		changed = true
	}
	if changed {
		c.daemon.PublishStatusChanged()
	}
}

// MarkStatusViewed flags a status as seen locally and publishes it. Viewed
// receipts to the sender are not sent yet; this only drives local state.
// Opening a status marks it viewed, so this also kicks off its media download
// (best-effort, background): viewed-but-never-downloaded rows cannot linger.
func (c *Client) MarkStatusViewed(ctx context.Context, statusID string) (appstore.StatusUpdate, error) {
	statusID = strings.TrimSpace(statusID)
	if statusID == "" {
		return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorInvalidArgument, "status_id is required")
	}
	updated, err := c.store.MarkStatusViewed(ctx, statusID)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	c.daemon.PublishStatusChanged()
	if updated.MediaKind != "" && updated.Kind != "text" && strings.TrimSpace(updated.MediaLocalPath) == "" {
		id := updated.ID
		go func() {
			if _, err := c.DownloadStatusMedia(c.backgroundContext(), id); err != nil {
				c.log.Warnf("Auto-download on viewed status %s: %v", id, err)
			}
		}()
	}
	return updated, nil
}

// PostStatus publishes a text or media status (story) to status@broadcast.
// Exactly one of text or path must be set; path reuses the send.media
// classifier (photo, video, audio; documents are rejected — statuses have no
// document form). Text statuses post as ExtendedTextMessage with a background
// color and font: a bare Conversation renders as "unsupported" on official
// clients. The post is stored as our own status update so it shows in the
// `status` feed; unlike chat sends it never touches the chats table.
func (c *Client) PostStatus(ctx context.Context, text, path, caption string, background uint32, font int32) (appstore.StatusUpdate, error) {
	client := c.currentClient()
	if client == nil {
		return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	}

	text = strings.TrimSpace(text)
	path = strings.TrimSpace(path)
	if (text == "") == (path == "") {
		return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorInvalidArgument, "exactly one of text or path is required")
	}

	messageID := client.GenerateMessageID()
	if text != "" {
		if background == 0 {
			background = defaultStatusBackground
		}
		statusMsg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:           proto.String(text),
			BackgroundArgb: proto.Uint32(background),
			TextArgb:       proto.Uint32(0xFFFFFFFF),
			Font:           statusFont(font),
		}}
		if _, err := client.SendMessage(ctx, types.StatusBroadcastJID, statusMsg); err != nil {
			return appstore.StatusUpdate{}, err
		}
		return c.storeOwnStatus(ctx, messageID, appstore.StatusUpdateInput{
			Kind:     "text",
			Text:     text,
			TextBG:   background,
			TextFont: int32(statusMsg.GetExtendedTextMessage().GetFont()),
		})
	}

	data, mimeType, _, mediaKind, _, err := readOutboundMedia(path, MediaSendOptions{})
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	if mediaKind == appstore.MediaKindDocument {
		return appstore.StatusUpdate{}, app.NewCommandError(app.CommandErrorInvalidArgument, "statuses cannot be documents: send a photo, video or audio file")
	}
	mediaDir := filepath.Join(c.paths.MediaCacheDir, "status")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return appstore.StatusUpdate{}, err
	}
	localPath := filepath.Join(mediaDir, safeMediaFileName("own-"+string(messageID), mediaExtension(mimeType)))
	if err := writeFileAtomic(localPath, data, 0o600); err != nil {
		return appstore.StatusUpdate{}, err
	}

	envelope, inner, uploadType, err := buildOutgoingMediaMessage(appstore.Message{
		Text:          caption,
		MediaKind:     mediaKind,
		MediaMimeType: mimeType,
	}, data, mimeType)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	resp, err := client.Upload(ctx, data, uploadType)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	fillOutgoingMediaUpload(inner, resp)
	if _, err := client.SendMessage(ctx, types.StatusBroadcastJID, envelope); err != nil {
		return appstore.StatusUpdate{}, err
	}
	payload, err := proto.Marshal(inner)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	stored, err := c.storeOwnStatus(ctx, messageID, appstore.StatusUpdateInput{
		Kind:          mediaKind,
		Text:          caption,
		MediaKind:     mediaKind,
		MediaMimeType: mimeType,
		MediaPayload:  payload,
	})
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	thumbW, thumbH := statusImageDimensions(mediaKind, data)
	updated, err := c.store.SetStatusMediaPath(ctx, stored.ID, localPath, "", thumbW, thumbH)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	return updated, nil
}

// DeleteStatus deletes one of our own statuses: revoke on WhatsApp (delete
// for everyone, like official clients) plus drop the local row. Others'
// statuses cannot be deleted.
func (c *Client) DeleteStatus(ctx context.Context, statusID string) error {
	client, err := c.requireConnectedClient()
	if err != nil {
		return err
	}
	statusID = strings.TrimSpace(statusID)
	if statusID == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "status_id is required")
	}
	status, err := c.store.GetStatusUpdate(ctx, statusID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status.SenderID) != "me" {
		return app.NewCommandError(app.CommandErrorRejected, "only your own statuses can be deleted")
	}
	externalID := strings.TrimPrefix(status.ID, "status:")
	if _, err := client.RevokeMessage(ctx, types.StatusBroadcastJID, types.MessageID(externalID)); err != nil {
		return err
	}
	if err := c.store.DeleteStatusUpdate(ctx, status.ID); err != nil {
		return err
	}
	c.daemon.PublishStatusChanged()
	return nil
}

func (c *Client) ListStatusViewers(ctx context.Context, statusID string) ([]appstore.StatusViewer, error) {
	return c.store.ListStatusViewers(ctx, strings.TrimSpace(statusID))
}

// SetStatusKeepSender pins (or unpins) a contact's expired statuses: kept
// senders grow an archived section in the Status tab instead of having their
// older statuses hidden once past 24h.
func (c *Client) SetStatusKeepSender(ctx context.Context, senderID string, kept bool) error {
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "sender_id is required")
	}
	if err := c.store.SetStatusKeepSender(ctx, senderID, kept); err != nil {
		return err
	}
	c.daemon.PublishStatusChanged()
	return nil
}

// ListKeptStatusSenders returns the sender ids with status keep enabled.
func (c *Client) ListKeptStatusSenders(ctx context.Context) ([]string, error) {
	return c.store.ListKeptStatusSenders(ctx)
}

// SetStatusMutedSender hides (or unhides) a contact's statuses: muted
// senders collect under the Status tab's Muted section instead of the main
// list. Local toggles apply immediately; the phone remains the source of
// truth and overwrites this set on the next full appstate sync.
func (c *Client) SetStatusMutedSender(ctx context.Context, senderID string, muted bool) error {
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "sender_id is required")
	}
	// Normalize to every store key for this sender (bare + LID/PN-resolved
	// canonical, see statusMuteKeys): unmuting must clear the same rows a
	// phone-side mute wrote, or an orphan row keeps the contact muted.
	// Unparseable input falls through as-is — ids like "me" are not JIDs
	// but are valid sender keys the tab reads back verbatim.
	keys := []string{senderID}
	if jid, err := types.ParseJID(senderID); err == nil && !jid.IsEmpty() {
		keys = c.statusMuteKeys(ctx, jid)
	}
	for _, key := range keys {
		if err := c.store.SetStatusMutedSender(ctx, key, muted); err != nil {
			return err
		}
	}
	c.daemon.PublishStatusChanged()
	return nil
}

// ListMutedStatusSenders returns the sender ids with status mute enabled.
func (c *Client) ListMutedStatusSenders(ctx context.Context) ([]string, error) {
	return c.store.ListMutedStatusSenders(ctx)
}

// statusMuteKeys returns every store key a status mute for jid must touch:
// the bare JID (the form status rows store their sender in) plus the
// LID→PN-resolved canonical form when it differs. Status senders keep
// whichever form the message arrived in while appstate events may use the
// other; keying both means the tab's mute lookup matches either way.
func (c *Client) statusMuteKeys(ctx context.Context, jid types.JID) []string {
	bare := bareAvatarJID(jid).String()
	if bare == "" {
		return nil
	}
	keys := []string{bare}
	if canonical := c.canonicalParticipantJID(ctx, jid); canonical != "" && canonical != bare {
		keys = append(keys, canonical)
	}
	return keys
}

// handleUserStatusMuteEvent mirrors a status mute/unmute made on the phone
// (or another linked device) into the local muted set.
func (c *Client) handleUserStatusMuteEvent(ctx context.Context, evt *events.UserStatusMute) {
	if evt == nil || evt.JID.IsEmpty() || evt.Action == nil {
		return
	}
	keys := c.statusMuteKeys(ctx, evt.JID)
	if len(keys) == 0 {
		return
	}
	for _, sender := range keys {
		if err := c.store.SetStatusMutedSender(ctx, sender, evt.Action.GetMuted()); err != nil {
			c.log.Warnf("Failed to apply status mute for %s: %v", sender, err)
			return
		}
	}
	c.daemon.PublishStatusChanged()
}

// sameStringSet reports whether a and b hold the same ids, ignoring order
// and blanks (ReplaceMutedStatusSenders skips blanks too, so they must not
// count toward a difference).
func sameStringSet(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, id := range a {
		if strings.TrimSpace(id) == "" {
			continue
		}
		set[id] = struct{}{}
	}
	count := 0
	for _, id := range b {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, ok := set[id]; !ok {
			return false
		}
		count++
	}
	return count == len(set)
}

// reconcileMutedStatusesFromEvents replaces the local muted set with the
// phone's full-snapshot state: unlisted senders are unmuted. Runs on every
// Connected via reconcileRegularAppState, so the tab matches the phone
// within seconds of connecting.
func (c *Client) reconcileMutedStatusesFromEvents(ctx context.Context, eventsToDispatch []any) {
	muted := make(map[string]struct{})
	for _, raw := range eventsToDispatch {
		evt, ok := raw.(*events.UserStatusMute)
		if !ok || evt.JID.IsEmpty() || evt.Action == nil {
			continue
		}
		keys := c.statusMuteKeys(ctx, evt.JID)
		if len(keys) == 0 {
			continue
		}
		for _, sender := range keys {
			if evt.Action.GetMuted() {
				muted[sender] = struct{}{}
			} else {
				delete(muted, sender)
			}
		}
	}
	ids := make([]string, 0, len(muted))
	for id := range muted {
		ids = append(ids, id)
	}
	// Skip the write (and the invalidation storm) when the snapshot matches
	// what is already stored — the common case on every connect.
	if current, err := c.store.ListMutedStatusSenders(ctx); err == nil && sameStringSet(current, ids) {
		return
	}
	if err := c.store.ReplaceMutedStatusSenders(ctx, ids); err != nil {
		c.log.Warnf("Failed to reconcile muted statuses from app state: %v", err)
		return
	}
	c.daemon.PublishStatusChanged()
}

// ReplyToStatus sends a chat message to the status author quoting their
// status, the way official clients reply to stories: the reply lands as an
// ordinary DM carrying the status as its quote context.
func (c *Client) ReplyToStatus(ctx context.Context, statusID, text string) (appstore.SavedTextMessage, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	statusID = strings.TrimSpace(statusID)
	if statusID == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "status_id is required")
	}
	if strings.TrimSpace(text) == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "text is required")
	}
	status, err := c.store.GetStatusUpdate(ctx, statusID)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	if strings.TrimSpace(status.SenderID) == "" || strings.TrimSpace(status.SenderID) == "me" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorRejected, "only others' statuses can be replied to")
	}
	chat, err := c.EnsureDirectChat(ctx, status.SenderID)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	rpcArrival := time.Now()
	messageID := client.GenerateMessageID()
	quoteText := status.Text
	if quoteText == "" {
		quoteText = statusFallbackText(status.Kind)
	}
	saved, err := c.store.SaveTextMessage(ctx, appstore.TextMessageInput{
		ID:          internalMessageIDForChat(chat.ID, messageID),
		ChatID:      chat.ID,
		SenderID:    "me",
		Text:        text,
		Timestamp:   time.Now(),
		Direction:   appstore.DirectionOutgoing,
		Status:      appstore.StatusPending,
		CountUnread: false,
		ReplyTo: appstore.MessageReply{
			MessageID:     strings.TrimPrefix(status.ID, "status:"),
			SenderID:      status.SenderID,
			SenderName:    status.SenderName,
			Text:          quoteText,
			MediaKind:     status.MediaKind,
			MediaMimeType: status.MediaMimeType,
			Direction:     appstore.DirectionIncoming,
		},
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	if saved.Inserted {
		c.beginSendTiming(saved.Message.ID, rpcArrival)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	c.signalSendQueue()
	return saved, nil
}

// statusFallbackText is the quote text for statuses without a caption.
func statusFallbackText(kind string) string {
	switch kind {
	case appstore.MediaKindImage:
		return "📷 Photo status"
	case appstore.MediaKindVideo:
		return "🎥 Video status"
	case appstore.MediaKindVoice:
		return "🎤 Voice status"
	case appstore.MediaKindAudio:
		return "🎵 Audio status"
	default:
		return "Status update"
	}
}

// storeOwnStatus records a status we posted so it shows in the `status` feed
// next to everyone else's. The ID reuses the sent message ID.
func (c *Client) storeOwnStatus(ctx context.Context, messageID types.MessageID, input appstore.StatusUpdateInput) (appstore.StatusUpdate, error) {
	input.ID = "status:" + string(messageID)
	input.SenderID = "me"
	input.Timestamp = time.Now()
	stored, _, err := c.store.SaveStatusUpdate(ctx, input)
	if err != nil {
		return appstore.StatusUpdate{}, err
	}
	c.daemon.PublishStatusChanged()
	return stored, nil
}

// ingestStatusUpdate stores a contact status outside the chat store and
// publishes it to the `status` view. Text, photo, video and audio statuses
// are kept; protocol noise (reactions, votes, ...) is ignored. Nothing here
// notifies or counts unread — statuses are browsed, not pushed.
func (c *Client) ingestStatusUpdate(ctx context.Context, evt *events.Message) {
	if evt == nil || evt.Message == nil || evt.Info.ID == "" {
		return
	}
	sender := senderID(evt.Info)
	if sender == "" {
		return
	}
	var senderName string
	if sender != "me" {
		if jid, err := types.ParseJID(sender); err == nil {
			senderName = c.senderName(ctx, jid)
		}
	}

	input := appstore.StatusUpdateInput{
		ID:        "status:" + string(evt.Info.ID),
		SenderID:  sender,
		Timestamp: c.messageTimestamp(evt.Info, ingestOptions{}, evt.SourceWebMsg),
	}
	if input.Timestamp.IsZero() {
		input.Timestamp = evt.Info.Timestamp
	}

	if text := strings.TrimSpace(textFromMessage(evt.Message)); text != "" {
		input.Kind = "text"
		input.Text = text
		input.TextBG, input.TextFont = statusTextStyle(evt.Message)
	} else if kind, mime, payload, duration, size, caption, ok := statusMediaPayload(evt.Message); ok {
		input.Kind = kind
		input.Text = caption
		input.MediaKind = kind
		input.MediaMimeType = mime
		input.MediaPayload = payload
		input.MediaDurationSecs = duration
		input.MediaSizeBytes = size
	} else {
		return
	}
	if senderName != "" {
		input.SenderName = senderName
	}

	if stored, inserted, err := c.store.SaveStatusUpdate(ctx, input); err != nil {
		c.log.Warnf("Failed to store status %s: %v", input.ID, err)
		return
	} else if inserted {
		// Thumbnail-first: cache the sender's thumbnail (and image dims) at
		// ingest so the feed renders something before any full download.
		c.attachIngestedStatusThumb(ctx, stored.ID, stored.MediaKind, stored.MediaPayload)
		c.daemon.PublishStatusChanged()
	}
}

// statusMirrorCleanupKey marks the one-time purge of the status@broadcast
// chat row the retired mirror experiment created.
const statusMirrorCleanupKey = "status_mirror_cleanup_v1"

// misfiledChatsCleanupKey marks the one-time purge of newsletter rows the old
// ingest filed as chats. Channel posts live server-side (the Channels tab
// fetches them live), so nothing of value is lost.
const misfiledChatsCleanupKey = "misfilled_chats_cleanup_v1"

// pruneStatusBroadcastMirror drops the status@broadcast chat the retired
// mirror experiment filed statuses into. Its messages duplicate the Status
// tab, so nothing of value is lost; statuses keep living in status_updates.
// One-time via an app_state marker; statuses routed to chats never return.
func (c *Client) pruneStatusBroadcastMirror(ctx context.Context) {
	if done, err := c.store.GetAppStateValue(ctx, statusMirrorCleanupKey); err == nil && done != "" {
		return
	}
	existed, err := c.store.DeleteChat(ctx, types.StatusBroadcastJID.String())
	if err != nil {
		c.log.Warnf("Failed to prune status@broadcast mirror chat: %v", err)
		return
	}
	if err := c.store.SetAppStateValue(ctx, statusMirrorCleanupKey, "1"); err != nil {
		c.log.Warnf("Failed to record status mirror cleanup: %v", err)
	}
	if existed {
		c.daemon.PublishChatDeleted(types.StatusBroadcastJID.String())
	}
}

// pruneMisfiledNewsletterChats drops newsletter rows the old ingest filed as
// chats (with their stale unread badges). Content stays on the server behind
// the Channels tab. One-time via an app_state marker.
func (c *Client) pruneMisfiledNewsletterChats(ctx context.Context) {
	if done, err := c.store.GetAppStateValue(ctx, misfiledChatsCleanupKey); err == nil && done != "" {
		return
	}
	ids, err := c.store.ChatIDsWithServer(ctx, types.NewsletterServer)
	if err != nil {
		c.log.Warnf("Failed to list newsletter chats for purge: %v", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if existed, err := c.store.DeleteChat(ctx, id); err != nil {
			c.log.Warnf("Failed to purge newsletter chat %s: %v", id, err)
		} else if existed {
			c.daemon.PublishChatDeleted(id)
		}
	}
	if err := c.store.SetAppStateValue(ctx, misfiledChatsCleanupKey, "1"); err != nil {
		c.log.Warnf("Failed to record newsletter purge: %v", err)
	}
}

// backfillStatusThumbs attaches sender thumbnails to statuses stored before
// ingest-time thumbnails existed. Rows already carrying a thumbnail, a
// downloaded file, or no payload are skipped, so reruns are cheap; statuses
// expire after 24h, keeping the set small.
func (c *Client) backfillStatusThumbs(ctx context.Context) {
	statuses, err := c.store.ListStatusUpdates(ctx, 0)
	if err != nil {
		c.log.Warnf("Failed to list statuses for thumbnail backfill: %v", err)
		return
	}
	changed := false
	for _, st := range statuses {
		if ctx.Err() != nil {
			return
		}
		if st.MediaThumbnailLocalPath != "" || st.MediaLocalPath != "" || len(st.MediaPayload) == 0 {
			continue
		}
		c.attachIngestedStatusThumb(ctx, st.ID, st.MediaKind, st.MediaPayload)
		changed = true
	}
	if changed {
		c.daemon.PublishStatusChanged()
	}
}

// attachIngestedStatusThumb persists a freshly ingested status's sender
// thumbnail + image dimensions. Best-effort: ingest must never fail because
// a thumbnail could not be cached.
func (c *Client) attachIngestedStatusThumb(ctx context.Context, id, mediaKind string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	var thumb []byte
	switch mediaKind {
	case appstore.MediaKindImage:
		img := &waE2E.ImageMessage{}
		if err := proto.Unmarshal(payload, img); err != nil {
			return
		}
		thumb = img.GetJPEGThumbnail()
	case appstore.MediaKindVideo, appstore.MediaKindGIF:
		video := &waE2E.VideoMessage{}
		if err := proto.Unmarshal(payload, video); err != nil {
			return
		}
		thumb = video.GetJPEGThumbnail()
	default:
		return
	}
	if len(thumb) == 0 {
		return
	}
	var width, height int32
	if mediaKind == appstore.MediaKindImage {
		width, height = decodedImageDimensions(thumb)
	}
	if _, err := c.store.SetStatusMediaPath(ctx, id, "", c.saveStatusThumbnail(id, thumb), width, height); err != nil {
		c.log.Warnf("Failed to cache status thumbnail for %s: %v", id, err)
	}
}

// statusTextStyle extracts a text status's background color (ARGB, 0 when
// unset) and WhatsApp font id from its ExtendedTextMessage. Plain
// Conversation texts carry neither.
func statusTextStyle(msg *waE2E.Message) (uint32, int32) {
	extended := msg.GetExtendedTextMessage()
	if extended == nil {
		return 0, 0
	}
	return extended.GetBackgroundArgb(), int32(extended.GetFont())
}

// statusMediaPayload extracts the storable media facts from a status message:
// the gallery kind, MIME type, wire payload (media keys for later download),
// duration, size and caption. View-once statuses stay tombstoned like
// view-once chat media: the payload is deliberately not kept.
func statusMediaPayload(msg *waE2E.Message) (kind, mime string, payload []byte, duration int32, size int64, caption string, ok bool) {
	if msg == nil {
		return "", "", nil, 0, 0, "", false
	}
	if img := msg.GetImageMessage(); img != nil {
		payload, err := proto.Marshal(img)
		if err != nil {
			return "", "", nil, 0, 0, "", false
		}
		return appstore.MediaKindImage, defaultMime(img.GetMimetype(), "image/jpeg"),
			payload, 0, int64(img.GetFileLength()), img.GetCaption(), true
	}
	if video := msg.GetVideoMessage(); video != nil {
		payload, err := proto.Marshal(video)
		if err != nil {
			return "", "", nil, 0, 0, "", false
		}
		kind := appstore.MediaKindVideo
		if video.GetGifPlayback() {
			kind = appstore.MediaKindGIF
		}
		return kind, defaultMime(video.GetMimetype(), "video/mp4"),
			payload, int32(video.GetSeconds()), int64(video.GetFileLength()), video.GetCaption(), true
	}
	if audio := msg.GetAudioMessage(); audio != nil {
		payload, err := proto.Marshal(audio)
		if err != nil {
			return "", "", nil, 0, 0, "", false
		}
		kind := appstore.MediaKindAudio
		if audio.GetPTT() {
			kind = appstore.MediaKindVoice
		}
		return kind, defaultMime(audio.GetMimetype(), "audio/ogg; codecs=opus"),
			payload, int32(audio.GetSeconds()), int64(audio.GetFileLength()), "", true
	}
	return "", "", nil, 0, 0, "", false
}
