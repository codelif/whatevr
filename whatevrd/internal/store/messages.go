package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"whatevrd/internal/textutil"
)

const (
	DirectionIncoming = "incoming"
	DirectionOutgoing = "outgoing"

	MediaKindImage   = "image"
	MediaKindSticker = "sticker"
	// MediaKindGIF is a VideoMessage with GifPlayback set: a muted, looping
	// clip that WhatsApp presents as a GIF even though the wire carries video.
	MediaKindGIF   = "gif"
	MediaKindVideo = "video"
	// MediaKindVideoNote is a PtvMessage: the round "instant video" recording.
	MediaKindVideoNote = "video_note"
	// MediaKindVoice is an AudioMessage with PTT set (a recorded voice note),
	// as opposed to MediaKindAudio, which is a shared audio file.
	MediaKindVoice    = "voice"
	MediaKindAudio    = "audio"
	MediaKindDocument = "document"

	// MediaKindLocation is a LocationMessage. The "media" it carries is the map
	// the daemon stitches for it, which is why it goes through the ordinary
	// download lifecycle rather than inventing a second one.
	MediaKindLocation = "location"
	// MediaKindLiveLocation is the opening LocationMessage of a live share. The
	// LiveLocationMessage updates that follow do not become rows of their own;
	// they move this one. See live_locations.go.
	MediaKindLiveLocation = "live_location"
	MediaKindContact      = "contact"
	MediaKindContacts     = "contacts"
	MediaKindPoll         = "poll"
	MediaKindGroupInvite  = "group_invite"
	MediaKindEvent        = "event"
	// MediaKindAlbum is the AlbumMessage header. Its children are ordinary rows
	// carrying album_parent_id, hidden from the transcript and delivered inside
	// this row instead.
	MediaKindAlbum = "album"
	// MediaKindInteractive covers the business message family: buttons, lists,
	// hydrated templates and interactive messages, which differ in wire shape
	// but render as the same card.
	MediaKindInteractive = "interactive"
	MediaKindProduct     = "product"
	MediaKindOrder       = "order"
	MediaKindPayment     = "payment"
	MediaKindStickerPack = "sticker_pack"
	MediaKindCallLog     = "call_log"
	// MediaKindSystem is a synthetic row for something that happened to the
	// chat rather than in it: a join, a subject change, a disappearing-timer
	// change, a security-code change. Never counted as unread.
	MediaKindSystem = "system"
	// MediaKindWaiting is a message we could not decrypt and have asked the
	// phone to resend. Unlike every other kind, this row is expected to be
	// replaced in place when the real message arrives under the same id.
	MediaKindWaiting = "waiting"
	// MediaKindUnsupported marks a real message whose payload whatevr cannot
	// render yet. The text column carries a human-readable label and
	// media_payload keeps the marshalled proto, so a later build that learns
	// the kind can upgrade the row instead of leaving it grey forever.
	MediaKindUnsupported = "unsupported"

	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusRead      = "read"
	StatusFailed    = "failed"
	StatusSent      = "sent"
)

// MessageMention is one @-mention: the mentioned participant's JID plus the
// display name resolved when the message was ingested. The display name may be
// empty when the participant was unknown at ingest; the renderer then falls
// back to the JID's user-part, exactly as WhatsApp shows an unknown number.
type MessageMention struct {
	JID         string
	DisplayName string
}

// Field/record separators for the messages.mentioned_jids column. These ASCII
// control chars never appear in JIDs or display names, so the pairs round-trip
// without a JSON dependency. Empty in/empty out is the common (no-mention) case.
const (
	mentionFieldSep  = "\x1f"
	mentionRecordSep = "\x1e"
)

func encodeMentions(mentions []MessageMention) string {
	if len(mentions) == 0 {
		return ""
	}
	records := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		jid := strings.TrimSpace(mention.JID)
		if jid == "" {
			continue
		}
		records = append(records, jid+mentionFieldSep+mention.DisplayName)
	}
	return strings.Join(records, mentionRecordSep)
}

func decodeMentions(raw string) []MessageMention {
	if raw == "" {
		return nil
	}
	records := strings.Split(raw, mentionRecordSep)
	mentions := make([]MessageMention, 0, len(records))
	for _, record := range records {
		jid, name, _ := strings.Cut(record, mentionFieldSep)
		if jid == "" {
			continue
		}
		mentions = append(mentions, MessageMention{JID: jid, DisplayName: name})
	}
	return mentions
}

// MentionJIDs pulls just the JIDs out of a mention list, in order. The send
// path uses these to populate ContextInfo.MentionedJID on the wire.
func MentionJIDs(mentions []MessageMention) []string {
	if len(mentions) == 0 {
		return nil
	}
	jids := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		jids = append(jids, mention.JID)
	}
	return jids
}

type Message struct {
	ID                    string
	ChatID                string
	SenderID              string
	SenderName            string
	SenderAvatarLocalPath string
	// SenderDevice is the sender's device id from the envelope: 0 is the
	// primary phone app, anything else a linked device.
	SenderDevice            uint16
	Text                    string
	TimestampUnix           int64
	SortMS                  int64
	Direction               string
	IsRead                  bool
	Status                  string
	MediaKind               string
	MediaMimeType           string
	MediaLocalPath          string
	MediaThumbnailLocalPath string
	MediaWidth              int32
	MediaHeight             int32
	MediaAnimated           bool
	MediaDownloadError      string
	MediaPayload            []byte
	MediaCacheKey           string
	MediaDurationSecs       int32
	MediaSizeBytes          int64
	MediaFileName           string
	MediaPageCount          int32
	// MediaWaveform holds the 64 amplitude buckets (0-100) WhatsApp ships with
	// a voice note, or the ones the daemon derived after download when the
	// sender omitted them. Empty for every other kind.
	MediaWaveform []byte
	// MediaPlayed records that we already sent a played receipt for an inbound
	// voice note, so marking it played twice is a no-op.
	MediaPlayed bool
	// PayloadJSON is the kind-specific structured payload (a location's
	// coordinates, a vCard's parsed fields, a poll's settings, a system event's
	// participants). Opaque here; decoded by the protocol layer into the
	// message item's nested object for that kind.
	PayloadJSON string
	// PayloadSummary is the one-line detail the ingest derived from that
	// payload: a poll's question, a place name, "Ana and 12 others joined". It
	// is a column rather than a field inside PayloadJSON so the preview paths
	// never parse JSON.
	PayloadSummary string
	// AlbumParentID names the AlbumMessage this media belongs to. A row with a
	// parent that exists is hidden from the transcript and delivered inside the
	// album row instead.
	AlbumParentID string
	AlbumIndex    int32
	// IsKept records a KeepInChatMessage naming this row: a disappearing
	// message somebody asked to keep.
	IsKept bool
	// IsViewOnce marks our own view-once sends. Inbound view-once media is
	// never stored as media (phone-only tombstone), so this only appears on
	// outgoing rows.
	IsViewOnce      bool
	IsRevoked       bool
	IsForwarded     bool
	IsEdited        bool
	IsStarred       bool
	PinnedAt        int64
	PinnedUntil     int64
	SendAttempts    int32
	LastSendError   string
	NextSendAttempt int64
	ReplyTo         MessageReply
	Reactions       []Reaction
	// @-mentioned participants with names resolved at ingest. Empty for the
	// vast majority of messages.
	Mentions []MessageMention
	// Poll is the live tally, attached at read time for poll rows only. It is
	// joined rather than denormalized because a snapshot rewritten on every
	// vote is a snapshot that can be stale.
	Poll *PollState
	// Event is the RSVPs, attached at read time for event rows only, and joined
	// for the same reason a poll's tally is.
	Event *EventState
	// Album is the pictures this row groups, attached at read time for album
	// rows only. Each one is a whole message: the tiles are the children, not a
	// copy of them.
	Album []Message
	// StickerPack is what the local library currently knows about the pack a
	// sticker-pack row shares, attached at read time for the same reason a
	// poll's tally is: installing a pack from the picker must not leave a card
	// elsewhere in the transcript still offering to add it.
	StickerPack *StickerPackState
}

// StickerPackState is the library's answer about a shared pack. Known is false
// for a pack the daemon cannot find at all, which is the normal case for one
// somebody made on their own phone: there is nothing to install by id, and a
// card that offered anyway would be offering a button that fails.
type StickerPackState struct {
	Known     bool
	Installed bool
}

type MessageReply struct {
	MessageID     string
	SenderID      string
	SenderName    string
	Text          string
	MediaKind     string
	MediaMimeType string
	Direction     string
}

type MediaMessageInput struct {
	TextMessageInput
	MediaMimeType           string
	MediaKind               string
	MediaLocalPath          string
	MediaThumbnailLocalPath string
	MediaWidth              int32
	MediaHeight             int32
	MediaAnimated           bool
	MediaPayload            []byte
	MediaCacheKey           string
	MediaDurationSecs       int32
	MediaSizeBytes          int64
	MediaFileName           string
	MediaPageCount          int32
	MediaWaveform           []byte
	PayloadSummary          string
	AlbumParentID           string
	AlbumIndex              int32
	// IsViewOnce marks an outbound media message sent as view-once.
	IsViewOnce bool
}

type ReadCandidate struct {
	InternalID    string
	ExternalID    string
	ChatID        string
	SenderID      string
	TimestampUnix int64
	SortMS        int64
}

type TextMessageInput struct {
	ID             string
	ChatID         string
	ChatName       string
	ChatNameSource string
	SenderID       string
	SenderName     string
	// SenderDevice is the sender's device id (see Message).
	SenderDevice uint16
	Text         string
	Timestamp    time.Time
	Direction    string
	Status       string
	IsGroup      bool
	CountUnread  bool
	IsForwarded  bool
	ReplyTo      MessageReply
	Mentions     []MessageMention
	// PayloadJSON is the row's structured payload. It sits here rather than on
	// MediaMessageInput because a payload belongs to the message, not to its
	// media: a link preview rides an ordinary text row, whose kind stays
	// `text` because the text is still the message.
	PayloadJSON string
}

type SavedTextMessage struct {
	Message  Message
	Chat     Chat
	Inserted bool
}

type MessageTimestampCorrection struct {
	Message Message
	Chat    Chat
	Changed bool
}

// waitingPlaceholderUpgrade opens the one conflict clause that is not DO
// NOTHING.
//
// Every other repeat of a message id is a duplicate and is dropped, which is
// what keeps a resend, a history-sync backfill and a live delivery of the same
// message from becoming three rows. A `waiting` placeholder is the exception:
// it is not another copy of the message, it is the hole the message left, and
// when the message finally arrives it has the *same id*. Dropping it would
// leave the placeholder standing forever with the real message thrown away.
//
// It updates rather than deleting and reinserting so the row keeps its stored
// sort key: a placeholder that vanished and came back would jump past
// everything that arrived while it waited.
//
// The fields common to every kind live here; each save path appends its own and
// closes with the WHERE that limits all of it to a placeholder.
const waitingPlaceholderUpgrade = `
		ON CONFLICT(id) DO UPDATE SET
			sender_id = excluded.sender_id,
			direction = excluded.direction,
			status = excluded.status,
			media_kind = excluded.media_kind,
			payload_summary = excluded.payload_summary,
			-- The original send time, which the placeholder already carries and
			-- a resend does not: a message must not move to where it was
			-- re-delivered.
			timestamp = MIN(messages.timestamp, excluded.timestamp),
			sort_ms = MIN(messages.sort_ms, excluded.sort_ms),
			-- Somebody who already looked at the placeholder has read this
			-- message; the chat's unread count was settled when the placeholder
			-- landed and is not touched again.
			is_read = CASE WHEN messages.is_read = 1 THEN 1 ELSE excluded.is_read END,`

// waitingPlaceholderExists reports whether the id already holds a placeholder
// for a message that had not arrived yet. It is asked before the insert,
// because afterwards the row has been overwritten and there is no way to tell
// an upgrade from an ordinary insert.
func waitingPlaceholderExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var kind string
	err := tx.QueryRowContext(ctx, `SELECT media_kind FROM messages WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return kind == MediaKindWaiting, nil
}

func normalizeTextMessageInput(input TextMessageInput) (TextMessageInput, error) {
	if input.ID == "" {
		return TextMessageInput{}, errors.New("message id is required")
	}
	if input.ChatID == "" {
		return TextMessageInput{}, errors.New("chat id is required")
	}
	if input.SenderID == "" {
		input.SenderID = input.ChatID
	}
	input.SenderName = strings.TrimSpace(input.SenderName)
	if input.Timestamp.IsZero() {
		input.Timestamp = time.Now()
	}
	if input.Direction == "" {
		input.Direction = DirectionIncoming
	}
	if input.Status == "" {
		input.Status = StatusDelivered
	}
	return input, nil
}

func (db *DB) SaveTextMessage(ctx context.Context, input TextMessageInput) (SavedTextMessage, error) {
	defer db.timeOp("SaveTextMessage", time.Now())
	input, err := normalizeTextMessageInput(input)
	if err != nil {
		return SavedTextMessage{}, err
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return SavedTextMessage{}, err
	}
	defer tx.Rollback()

	saved, err := saveTextMessageTx(ctx, tx, input)
	if err != nil {
		return SavedTextMessage{}, err
	}

	if err := tx.Commit(); err != nil {
		return SavedTextMessage{}, err
	}
	return saved, nil
}

// saveTextMessageTx stores one already-normalized text message inside the
// caller's transaction.
// messageDeletedForMe reports whether this id was deleted on purpose. Backfill
// re-delivers messages the phone still has, so without this a message somebody
// deleted comes back the next time its conversation is synced.
func messageDeletedForMe(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM deleted_messages WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func saveTextMessageTx(ctx context.Context, tx *sql.Tx, input TextMessageInput) (SavedTextMessage, error) {
	if err := upsertChat(ctx, tx, input); err != nil {
		return SavedTextMessage{}, err
	}
	if err := upsertSender(ctx, tx, input.SenderID, input.SenderName); err != nil {
		return SavedTextMessage{}, err
	}

	deleted, err := messageDeletedForMe(ctx, tx, input.ID)
	if err != nil {
		return SavedTextMessage{}, err
	}
	if deleted {
		return SavedTextMessage{}, nil
	}

	upgrading, err := waitingPlaceholderExists(ctx, tx, input.ID)
	if err != nil {
		return SavedTextMessage{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, sender_device, text, timestamp, sort_ms, direction, is_read, status, is_forwarded, mentioned_jids, payload_json, reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`+waitingPlaceholderUpgrade+`
			text = excluded.text,
			sender_device = excluded.sender_device,
			is_forwarded = excluded.is_forwarded,
			mentioned_jids = excluded.mentioned_jids,
			payload_json = excluded.payload_json,
			reply_to_message_id = excluded.reply_to_message_id,
			reply_to_sender_id = excluded.reply_to_sender_id,
			reply_to_sender_name = excluded.reply_to_sender_name,
			reply_to_text = excluded.reply_to_text,
			reply_to_media_kind = excluded.reply_to_media_kind,
			reply_to_media_mime_type = excluded.reply_to_media_mime_type,
			reply_to_direction = excluded.reply_to_direction
		WHERE messages.media_kind = '`+MediaKindWaiting+`'
	`, input.ID, input.ChatID, input.SenderID, input.SenderDevice, input.Text, input.Timestamp.Unix(), input.Timestamp.UnixMilli(), input.Direction, boolToInt(!input.CountUnread), input.Status, boolToInt(input.IsForwarded), encodeMentions(input.Mentions), input.PayloadJSON,
		input.ReplyTo.MessageID, input.ReplyTo.SenderID, input.ReplyTo.SenderName, input.ReplyTo.Text, input.ReplyTo.MediaKind, input.ReplyTo.MediaMimeType, input.ReplyTo.Direction)
	if err != nil {
		return SavedTextMessage{}, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return SavedTextMessage{}, err
	}
	inserted := rowsAffected > 0

	if inserted {
		bump := input
		if upgrading {
			// The placeholder already counted this message. The chat's preview
			// still has to change, because it currently reads "Waiting for this
			// message" and the message is right here.
			bump.CountUnread = false
		}
		if err := bumpChatForInsertedMessage(ctx, tx, bump, previewSummary(input, input.Text)); err != nil {
			return SavedTextMessage{}, err
		}
	}

	message, err := getMessageTx(ctx, tx, input.ID)
	if err != nil {
		return SavedTextMessage{}, err
	}

	chat, err := getChatTx(ctx, tx, input.ChatID)
	if err != nil {
		return SavedTextMessage{}, err
	}

	return SavedTextMessage{Message: message, Chat: chat, Inserted: inserted}, nil
}

// bumpChatForInsertedMessage refreshes the chat row's name/last-message/unread
// bookkeeping after a brand-new message row was inserted.
func bumpChatForInsertedMessage(ctx context.Context, tx *sql.Tx, input TextMessageInput, lastMessage string) error {
	unreadIncrement := 0
	if input.CountUnread {
		unreadIncrement = 1
	}
	nameSource := normalizeChatNameSource(input.ChatNameSource)

	_, err := tx.ExecContext(ctx, `
		UPDATE chats
		SET name = CASE WHEN ? != '' AND chat_name_source_priority(?) >= chat_name_source_priority(name_source) THEN ? ELSE name END,
			name_source = CASE WHEN ? != '' AND chat_name_source_priority(?) >= chat_name_source_priority(name_source) THEN ? ELSE name_source END,
			last_message = CASE WHEN ? >= last_message_time THEN ? ELSE last_message END,
			last_message_direction = CASE WHEN ? >= last_message_time THEN ? ELSE last_message_direction END,
			last_message_status = CASE WHEN ? >= last_message_time THEN ? ELSE last_message_status END,
			last_message_time = CASE WHEN ? >= last_message_time THEN ? ELSE last_message_time END,
			unread_count = unread_count + ?,
			is_group = ?
		WHERE id = ?
	`, input.ChatName, nameSource, input.ChatName, input.ChatName, nameSource, nameSource, input.Timestamp.Unix(), lastMessage, input.Timestamp.Unix(), input.Direction, input.Timestamp.Unix(), input.Status, input.Timestamp.Unix(), input.Timestamp.Unix(), unreadIncrement, boolToInt(input.IsGroup), input.ChatID)
	return err
}

func upsertChat(ctx context.Context, tx *sql.Tx, input TextMessageInput) error {
	insertName := input.ChatName
	nameSource := normalizeChatNameSource(input.ChatNameSource)
	if insertName == "" {
		insertName = input.ChatID
		nameSource = ChatNameSourceRaw
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO chats (id, name, name_source, last_message, last_message_time, last_message_direction, last_message_status, unread_count, is_group)
		VALUES (?, ?, ?, '', 0, '', '', 0, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = CASE WHEN ? != '' AND chat_name_source_priority(?) >= chat_name_source_priority(chats.name_source) THEN ? ELSE chats.name END,
			name_source = CASE WHEN ? != '' AND chat_name_source_priority(?) >= chat_name_source_priority(chats.name_source) THEN ? ELSE chats.name_source END,
			is_group = excluded.is_group
	`, input.ChatID, insertName, nameSource, boolToInt(input.IsGroup), input.ChatName, nameSource, input.ChatName, input.ChatName, nameSource, nameSource)
	return err
}

func upsertSender(ctx context.Context, tx *sql.Tx, senderID, senderName string) error {
	if senderID == "" || senderID == "me" {
		return nil
	}
	senderName = strings.TrimSpace(senderName)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO senders (id, name)
		VALUES (?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE senders.name END
	`, senderID, senderName)
	return err
}

func getMessageTx(ctx context.Context, tx *sql.Tx, id string) (Message, error) {
	var message Message
	err := getMessageRow(ctx, tx, id, &message)
	return message, err
}

func getChatTx(ctx context.Context, tx *sql.Tx, id string) (Chat, error) {
	return getChatRow(ctx, tx, id)
}

func normalizeMediaMessageInput(input MediaMessageInput) (MediaMessageInput, error) {
	normalized, err := normalizeTextMessageInput(input.TextMessageInput)
	if err != nil {
		return MediaMessageInput{}, err
	}
	input.TextMessageInput = normalized
	if input.MediaPayload == nil {
		input.MediaPayload = []byte{}
	}
	if input.MediaWaveform == nil {
		input.MediaWaveform = []byte{}
	}
	// Sticker rows must carry their content cache key so the indexed
	// DownloadedStickerPathByCacheKey lookup can reuse downloads; derive it
	// here when the caller didn't. Underivable payloads keep an empty key.
	if input.MediaKind == MediaKindSticker && input.MediaCacheKey == "" {
		if key, err := StickerCacheKeyFromPayload(input.MediaPayload); err == nil {
			input.MediaCacheKey = key
		}
	}
	return input, nil
}

func (db *DB) SaveMediaMessage(ctx context.Context, input MediaMessageInput) (SavedTextMessage, error) {
	defer db.timeOp("SaveMediaMessage", time.Now())
	input, err := normalizeMediaMessageInput(input)
	if err != nil {
		return SavedTextMessage{}, err
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return SavedTextMessage{}, err
	}
	defer tx.Rollback()

	saved, err := saveMediaMessageTx(ctx, tx, input)
	if err != nil {
		return SavedTextMessage{}, err
	}

	if err := tx.Commit(); err != nil {
		return SavedTextMessage{}, err
	}
	return saved, nil
}

// saveMediaMessageTx stores one already-normalized media message inside the
// caller's transaction.
func saveMediaMessageTx(ctx context.Context, tx *sql.Tx, input MediaMessageInput) (SavedTextMessage, error) {
	lastMessage := mediaInputSummary(input)

	// A picture the album above it will draw is not a new message in its own
	// right. It reads as already read, and further down it skips the chat bump
	// entirely, so five photos sent at once are one line in the chat list and
	// one number on the badge rather than five of each.
	hiddenInAlbum := albumChildIsHidden(ctx, tx, input.AlbumParentID)
	if hiddenInAlbum {
		input.CountUnread = false
	}

	if err := upsertChat(ctx, tx, input.TextMessageInput); err != nil {
		return SavedTextMessage{}, err
	}
	if err := upsertSender(ctx, tx, input.SenderID, input.SenderName); err != nil {
		return SavedTextMessage{}, err
	}

	deleted, err := messageDeletedForMe(ctx, tx, input.ID)
	if err != nil {
		return SavedTextMessage{}, err
	}
	if deleted {
		return SavedTextMessage{}, nil
	}

	upgrading, err := waitingPlaceholderExists(ctx, tx, input.ID)
	if err != nil {
		return SavedTextMessage{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, sender_device, text, timestamp, sort_ms, direction, is_read, status, is_forwarded, mentioned_jids, media_kind, media_mime_type, media_local_path, media_thumbnail_local_path, media_width, media_height, media_animated, media_payload, media_cache_key, media_duration_secs, media_size_bytes, media_file_name, media_page_count, media_waveform, is_view_once, payload_json, payload_summary, album_parent_id, album_index, reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`+waitingPlaceholderUpgrade+`
			text = excluded.text,
			sender_device = excluded.sender_device,
			is_forwarded = excluded.is_forwarded,
			is_view_once = excluded.is_view_once,
			mentioned_jids = excluded.mentioned_jids,
			media_mime_type = excluded.media_mime_type,
			media_local_path = excluded.media_local_path,
			media_thumbnail_local_path = excluded.media_thumbnail_local_path,
			media_width = excluded.media_width,
			media_height = excluded.media_height,
			media_animated = excluded.media_animated,
			media_payload = excluded.media_payload,
			media_cache_key = excluded.media_cache_key,
			media_duration_secs = excluded.media_duration_secs,
			media_size_bytes = excluded.media_size_bytes,
			media_file_name = excluded.media_file_name,
			media_page_count = excluded.media_page_count,
			media_waveform = excluded.media_waveform,
			payload_json = excluded.payload_json,
			album_parent_id = excluded.album_parent_id,
			album_index = excluded.album_index,
			reply_to_message_id = excluded.reply_to_message_id,
			reply_to_sender_id = excluded.reply_to_sender_id,
			reply_to_sender_name = excluded.reply_to_sender_name,
			reply_to_text = excluded.reply_to_text,
			reply_to_media_kind = excluded.reply_to_media_kind,
			reply_to_media_mime_type = excluded.reply_to_media_mime_type,
			reply_to_direction = excluded.reply_to_direction
		WHERE messages.media_kind = '`+MediaKindWaiting+`'
	`, input.ID, input.ChatID, input.SenderID, input.SenderDevice, input.Text, input.Timestamp.Unix(), input.Timestamp.UnixMilli(), input.Direction,
		boolToInt(!input.CountUnread), input.Status, boolToInt(input.IsForwarded), encodeMentions(input.Mentions), input.MediaKind, input.MediaMimeType, input.MediaLocalPath, input.MediaThumbnailLocalPath, input.MediaWidth, input.MediaHeight, boolToInt(input.MediaAnimated), input.MediaPayload, input.MediaCacheKey,
		input.MediaDurationSecs, input.MediaSizeBytes, input.MediaFileName, input.MediaPageCount, input.MediaWaveform, boolToInt(input.IsViewOnce),
		input.PayloadJSON, input.PayloadSummary, input.AlbumParentID, input.AlbumIndex,
		input.ReplyTo.MessageID, input.ReplyTo.SenderID, input.ReplyTo.SenderName, input.ReplyTo.Text, input.ReplyTo.MediaKind, input.ReplyTo.MediaMimeType, input.ReplyTo.Direction)
	if err != nil {
		return SavedTextMessage{}, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return SavedTextMessage{}, err
	}
	inserted := rowsAffected > 0

	if inserted && !hiddenInAlbum {
		bump := input.TextMessageInput
		if upgrading {
			// The placeholder already counted it; only the preview still needs
			// to change. See waitingPlaceholderUpgrade.
			bump.CountUnread = false
		}
		if err := bumpChatForInsertedMessage(ctx, tx, bump, previewSummary(input.TextMessageInput, lastMessage)); err != nil {
			return SavedTextMessage{}, err
		}
	}

	message, err := getMessageTx(ctx, tx, input.ID)
	if err != nil {
		return SavedTextMessage{}, err
	}

	chat, err := getChatTx(ctx, tx, input.ChatID)
	if err != nil {
		return SavedTextMessage{}, err
	}

	return SavedTextMessage{Message: message, Chat: chat, Inserted: inserted}, nil
}

// MessageSaveItem is one entry in a SaveMessages batch; exactly one of Text or
// Media must be set.
type MessageSaveItem struct {
	Text  *TextMessageInput
	Media *MediaMessageInput
}

// SaveMessages stores a batch of messages inside a single transaction. It
// exists for history-sync ingestion, where committing (and under
// synchronous=FULL, fsyncing) once per message dominated sync time. Results
// are returned in input order. The whole batch commits or rolls back as one;
// callers should pre-validate inputs so the only failures are real DB errors.
func (db *DB) SaveMessages(ctx context.Context, items []MessageSaveItem) ([]SavedTextMessage, error) {
	if len(items) == 0 {
		return nil, nil
	}
	defer db.timeOp("SaveMessages", time.Now())

	normalized := make([]MessageSaveItem, 0, len(items))
	for _, item := range items {
		switch {
		case item.Text != nil:
			input, err := normalizeTextMessageInput(*item.Text)
			if err != nil {
				return nil, err
			}
			normalized = append(normalized, MessageSaveItem{Text: &input})
		case item.Media != nil:
			input, err := normalizeMediaMessageInput(*item.Media)
			if err != nil {
				return nil, err
			}
			normalized = append(normalized, MessageSaveItem{Media: &input})
		default:
			return nil, errors.New("message save item has neither text nor media input")
		}
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	saved := make([]SavedTextMessage, 0, len(normalized))
	for _, item := range normalized {
		var result SavedTextMessage
		if item.Text != nil {
			result, err = saveTextMessageTx(ctx, tx, *item.Text)
		} else {
			result, err = saveMediaMessageTx(ctx, tx, *item.Media)
		}
		if err != nil {
			return nil, err
		}
		saved = append(saved, result)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}

// previewSummary composes the chat-list last-message preview from a base
// summary: mentions are expanded to display names, and in group chats the
// author is prefixed ("You:" for our own messages, otherwise the sender name).
func previewSummary(input TextMessageInput, summary string) string {
	summary = textutil.ExpandMentions(summary, toTextutilMentions(input.Mentions))
	if !input.IsGroup {
		return summary
	}
	author := ""
	if input.Direction == DirectionOutgoing {
		author = "You"
	} else if name := strings.TrimSpace(input.SenderName); name != "" {
		author = name
	}
	if author == "" {
		return summary
	}
	return author + ": " + summary
}

func toTextutilMentions(mentions []MessageMention) []textutil.Mention {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]textutil.Mention, len(mentions))
	for i, m := range mentions {
		out[i] = textutil.Mention{JID: m.JID, DisplayName: m.DisplayName}
	}
	return out
}

// mediaInputSummary is the chat-list preview for one message being inserted.
// The rendering itself lives in PreviewLine (kinds.go) so the chat row, the
// wire fallback and a reconstructed quote all read the same.
func mediaInputSummary(input MediaMessageInput) string {
	return PreviewLine(PreviewFacts{
		Text:           input.Text,
		PayloadSummary: input.PayloadSummary,
		MediaKind:      input.MediaKind,
		MediaMimeType:  input.MediaMimeType,
		MediaFileName:  input.MediaFileName,
		DurationSecs:   input.MediaDurationSecs,
	})
}

func (db *DB) RecordUndecryptableMessageTimestamp(ctx context.Context, id, chatID, messageID, senderID string, timestamp time.Time) (MessageTimestampCorrection, error) {
	if id == "" || chatID == "" || messageID == "" || timestamp.IsZero() {
		return MessageTimestampCorrection{}, nil
	}
	timestampUnix := timestamp.Unix()
	if timestampUnix <= 0 {
		return MessageTimestampCorrection{}, nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return MessageTimestampCorrection{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO undecryptable_messages (id, chat_id, message_id, sender_id, timestamp)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			chat_id = excluded.chat_id,
			message_id = excluded.message_id,
			sender_id = CASE WHEN excluded.sender_id != '' THEN excluded.sender_id ELSE undecryptable_messages.sender_id END,
			timestamp = MIN(undecryptable_messages.timestamp, excluded.timestamp),
			updated_at = unixepoch()
	`, id, chatID, messageID, senderID, timestampUnix); err != nil {
		return MessageTimestampCorrection{}, err
	}

	var effectiveTimestampUnix int64
	if err := tx.QueryRowContext(ctx, `
		SELECT timestamp
		FROM undecryptable_messages
		WHERE id = ?
	`, id).Scan(&effectiveTimestampUnix); err != nil {
		return MessageTimestampCorrection{}, err
	}

	// Only ever earlier, and only for an id that actually failed to decrypt, so
	// this moves a resend back to when it was sent rather than where it landed.
	// The sort key has to move with it or the row would render in one place and
	// page from another.
	result, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET timestamp = ?, sort_ms = ?
		WHERE id = ? AND timestamp > ?
	`, effectiveTimestampUnix, effectiveTimestampUnix*1000, id, effectiveTimestampUnix)
	if err != nil {
		return MessageTimestampCorrection{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return MessageTimestampCorrection{}, err
	}

	var correction MessageTimestampCorrection
	if rowsAffected > 0 {
		if err := recomputeChatSummaryTx(ctx, tx, chatID); err != nil {
			return MessageTimestampCorrection{}, err
		}
		message, err := getMessageTx(ctx, tx, id)
		if err != nil {
			return MessageTimestampCorrection{}, err
		}
		chat, err := getChatTx(ctx, tx, chatID)
		if err != nil {
			return MessageTimestampCorrection{}, err
		}
		correction = MessageTimestampCorrection{Message: message, Chat: chat, Changed: true}
	}

	if err := tx.Commit(); err != nil {
		return MessageTimestampCorrection{}, err
	}
	return correction, nil
}

func (db *DB) LookupUndecryptableMessageTimestamp(ctx context.Context, id string) (time.Time, bool, error) {
	if id == "" {
		return time.Time{}, false, nil
	}
	var timestampUnix int64
	err := db.reader().QueryRowContext(ctx, `
		SELECT timestamp
		FROM undecryptable_messages
		WHERE id = ?
	`, id).Scan(&timestampUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if timestampUnix <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(timestampUnix, 0), true, nil
}

func (db *DB) PruneUndecryptableMessageTimestamps(ctx context.Context, olderThan time.Time) error {
	if olderThan.IsZero() {
		return nil
	}
	_, err := db.conn.ExecContext(ctx, `
		DELETE FROM undecryptable_messages
		WHERE created_at < ?
	`, olderThan.Unix())
	return err
}

// recomputeChatSummaryTx rebuilds a chat's last-message preview from its newest
// surviving message. The preview is composed in Go (rather than SQL) so it stays
// consistent with the insert path: mentions are expanded and, in groups, the
// author is prefixed. Deleted messages tombstone to a fixed string with no
// author prefix.
func recomputeChatSummaryTx(ctx context.Context, tx *sql.Tx, chatID string) error {
	var latestID string
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM messages
		WHERE chat_id = ?
		ORDER BY sort_ms DESC, id DESC
		LIMIT 1
	`, chatID).Scan(&latestID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err := tx.ExecContext(ctx, `
			UPDATE chats
			SET last_message = '', last_message_time = 0, last_message_direction = '', last_message_status = ''
			WHERE id = ?
		`, chatID)
		return err
	}
	if err != nil {
		return err
	}

	latest, err := getMessageTx(ctx, tx, latestID)
	if err != nil {
		return err
	}

	summary := RevokedPreview
	// A revoked row whose content was kept (anti-delete) previews like any
	// other message; only a true tombstone shows the deleted line.
	if !latest.IsRevoked || latest.Text != "" || latest.MediaKind != "" {
		chat, err := getChatTx(ctx, tx, chatID)
		if err != nil {
			return err
		}
		preview := latest
		preview.IsRevoked = false
		summary = previewSummary(TextMessageInput{
			IsGroup:    chat.IsGroup,
			Direction:  latest.Direction,
			SenderName: latest.SenderName,
			Mentions:   latest.Mentions,
		}, MessagePreviewLine(preview))
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE chats
		SET last_message = ?, last_message_time = ?, last_message_direction = ?, last_message_status = ?
		WHERE id = ?
	`, summary, latest.TimestampUnix, latest.Direction, latest.Status, chatID)
	return err
}

// messageSelectPrefix is the SELECT + JOIN block shared by message list
// queries. Its column order matches scanMessageRows exactly; append a WHERE /
// ORDER BY / LIMIT clause to use it.
const messageSelectPrefix = `
	SELECT m.id, m.chat_id, m.sender_id, m.sender_device,
	       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
	       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
	       m.text, m.timestamp, m.sort_ms, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated, m.media_download_error,
	       m.media_duration_secs, m.media_size_bytes, m.media_file_name, m.media_page_count, m.media_waveform, m.media_played,
	       m.payload_json, m.payload_summary, m.album_parent_id, m.album_index, m.is_kept, m.is_view_once,
	       m.reply_to_message_id, m.reply_to_sender_id, m.reply_to_sender_name, m.reply_to_text, m.reply_to_media_kind, m.reply_to_media_mime_type, m.reply_to_direction,
	       m.send_attempts, m.last_send_error, m.next_send_attempt, m.is_revoked, m.is_edited, m.is_starred, m.pinned_at, m.pinned_until, m.mentioned_jids
	FROM messages m
	LEFT JOIN senders s ON s.id = m.sender_id
	LEFT JOIN chats c ON c.id = m.sender_id
	LEFT JOIN avatars sa ON sa.subject_kind = 'sender' AND sa.subject_id = m.sender_id
	LEFT JOIN avatars ca ON ca.subject_kind = 'chat' AND ca.subject_id = m.sender_id
`

func (db *DB) ListMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]Message, error) {
	defer db.timeOp("ListMessages", time.Now())
	if limit <= 0 {
		limit = 50
	}

	query := messageSelectPrefix + `
		WHERE m.chat_id = ?
	` + albumChildExclusion
	args := []any{chatID}

	if beforeMessageID != "" {
		beforeSortMS, beforeID, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}

		query += `
			AND (m.sort_ms < ? OR (m.sort_ms = ? AND m.id < ?))
		`
		args = append(args, beforeSortMS, beforeSortMS, beforeID)
	}

	query += `
		ORDER BY m.sort_ms DESC, m.id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
		return nil, err
	}
	if err := db.attachMessageExtras(ctx, db.reader(), messages); err != nil {
		return nil, err
	}

	reverseMessages(messages)
	return messages, nil
}

// ListMessagesForExport returns a chat's transcript oldest first for the
// official .txt export shape.
func (db *DB) ListMessagesForExport(ctx context.Context, chatID string) ([]Message, error) {
	defer db.timeOp("ListMessagesForExport", time.Now())
	rows, err := db.reader().QueryContext(ctx, messageSelectPrefix+`
		WHERE m.chat_id = ?
		ORDER BY m.timestamp ASC, m.rowid ASC
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, 0)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// ListVideoPosterCandidates returns downloaded rectangular videos and GIFs
// newest first. Video notes deliberately keep their sender-supplied thumbnail
// and circular presentation.
func (db *DB) ListVideoPosterCandidates(ctx context.Context) ([]Message, error) {
	defer db.timeOp("ListVideoPosterCandidates", time.Now())
	rows, err := db.reader().QueryContext(ctx, messageSelectPrefix+`
		WHERE m.media_kind IN (?, ?)
		  AND m.media_local_path != ''
		ORDER BY m.sort_ms DESC, m.id DESC
	`, MediaKindVideo, MediaKindGIF)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, 0)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// OldestStoredMessage returns the chat's oldest stored message with only the
// fields an on-demand history request needs (id, sender, direction,
// timestamp); ok=false when the chat has no messages.
func (db *DB) OldestStoredMessage(ctx context.Context, chatID string) (Message, bool, error) {
	if chatID == "" {
		return Message{}, false, nil
	}
	var msg Message
	err := db.reader().QueryRowContext(ctx, `
		SELECT id, chat_id, sender_id, direction, timestamp
		FROM messages
		WHERE chat_id = ?
		ORDER BY sort_ms ASC, id ASC
		LIMIT 1
	`, chatID).Scan(&msg.ID, &msg.ChatID, &msg.SenderID, &msg.Direction, &msg.TimestampUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	return msg, true, nil
}

// ListMessagesAfter returns up to limit messages strictly newer than
// afterMessageID within the same chat, oldest-first (closest to the cursor
// first). It is the newer-side mirror of ListMessages ("before"), and backs
// the messages view's directional (`newer`) extend. An empty afterMessageID
// yields nothing.
func (db *DB) ListMessagesAfter(ctx context.Context, chatID string, limit int, afterMessageID string) ([]Message, error) {
	defer db.timeOp("ListMessagesAfter", time.Now())
	if limit <= 0 {
		limit = 50
	}
	if afterMessageID == "" {
		return nil, nil
	}
	afterSortMS, afterID, err := db.messageCursor(ctx, afterMessageID)
	if err != nil {
		return nil, err
	}
	return db.listMessagesAroundSide(ctx, chatID, afterSortMS, afterID, limit, true)
}

func (db *DB) ListMessagesAround(ctx context.Context, chatID string, limit int, targetMessageID string) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if targetMessageID == "" {
		return nil, sql.ErrNoRows
	}

	target, err := db.GetMessage(ctx, targetMessageID)
	if err != nil {
		return nil, err
	}
	if target.ChatID != chatID {
		return nil, sql.ErrNoRows
	}
	// Jumping to a picture inside an album lands on the album. A search hit or
	// a starred row can name a child directly, and the child is not in the
	// transcript: anchoring on it would pin the window to a row that never
	// appears in it and leave the jump looking like it did nothing.
	if parentID := db.albumParentOf(ctx, target); parentID != "" {
		if target, err = db.GetMessage(ctx, parentID); err != nil {
			return nil, err
		}
	}
	if limit == 1 {
		return []Message{target}, nil
	}

	capacity := limit - 1
	olderDesc, err := db.listMessagesAroundSide(ctx, chatID, target.SortMS, target.ID, capacity, false)
	if err != nil {
		return nil, err
	}
	newerAsc, err := db.listMessagesAroundSide(ctx, chatID, target.SortMS, target.ID, capacity, true)
	if err != nil {
		return nil, err
	}

	olderTake := min(len(olderDesc), capacity/2)
	newerTake := min(len(newerAsc), capacity-olderTake)
	if olderTake+newerTake < capacity {
		olderTake = min(len(olderDesc), capacity-newerTake)
	}

	olderSelected := append([]Message(nil), olderDesc[:olderTake]...)
	reverseMessages(olderSelected)

	messages := make([]Message, 0, olderTake+1+newerTake)
	messages = append(messages, olderSelected...)
	messages = append(messages, target)
	messages = append(messages, newerAsc[:newerTake]...)
	return messages, nil
}

// ListMessagesAroundUnread returns a bounded window around the oldest message
// in the unread region. unreadCount comes from the chat badge snapshot: the
// region is the unreadCount most recent incoming, non-revoked messages.
func (db *DB) ListMessagesAroundUnread(ctx context.Context, chatID string, limit int, unreadCount int) ([]Message, string, error) {
	defer db.timeOp("ListMessagesAroundUnread", time.Now())
	if unreadCount <= 0 {
		return nil, "", sql.ErrNoRows
	}

	// The album exclusion is here for the same reason it is on the transcript:
	// this walks back unreadCount rows to find the oldest unread one, and a
	// picture inside an album is neither a row it can land on nor one that
	// added to the count. Counting them here would push the anchor forward by
	// the size of every album in the way.
	var anchorID string
	err := db.reader().QueryRowContext(ctx, `
        SELECT m.id
        FROM messages m
        WHERE m.chat_id = ?
          AND m.direction = ?
          AND m.is_revoked = 0
    `+albumChildExclusion+`
        ORDER BY m.sort_ms DESC, m.id DESC
        LIMIT 1 OFFSET ?
    `, chatID, DirectionIncoming, unreadCount-1).Scan(&anchorID)
	if err != nil {
		return nil, "", err
	}

	messages, err := db.ListMessagesAround(ctx, chatID, limit, anchorID)
	if err != nil {
		return nil, "", err
	}
	return messages, anchorID, nil
}

func (db *DB) listMessagesAroundSide(ctx context.Context, chatID string, sortMS int64, id string, limit int, newer bool) ([]Message, error) {
	if limit <= 0 {
		return nil, nil
	}

	comparison := `AND (m.sort_ms < ? OR (m.sort_ms = ? AND m.id < ?))`
	order := `ORDER BY m.sort_ms DESC, m.id DESC`
	if newer {
		comparison = `AND (m.sort_ms > ? OR (m.sort_ms = ? AND m.id > ?))`
		order = `ORDER BY m.sort_ms ASC, m.id ASC`
	}

	query := messageSelectPrefix + `
		WHERE m.chat_id = ?
	` + albumChildExclusion + comparison + `
		` + order + `
		LIMIT ?
	`

	rows, err := db.reader().QueryContext(ctx, query, chatID, sortMS, sortMS, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
		return nil, err
	}
	if err := db.attachMessageExtras(ctx, db.reader(), messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// messageCursor yields the pair a page walks from: the stored sort key and the
// id that breaks its ties.
func (db *DB) messageCursor(ctx context.Context, id string) (int64, string, error) {
	var sortMS int64
	err := db.reader().QueryRowContext(ctx, `SELECT sort_ms FROM messages WHERE id = ?`, id).Scan(&sortMS)
	return sortMS, id, err
}

func (db *DB) GetMessage(ctx context.Context, id string) (Message, error) {
	defer db.timeOp("GetMessage", time.Now())
	var message Message
	if err := getMessageRow(ctx, db.reader(), id, &message); err != nil {
		return message, err
	}
	if err := db.attachMessageExtrasOne(ctx, db.reader(), &message); err != nil {
		return message, err
	}
	return message, nil
}

func (db *DB) ListPendingOutgoingMessages(ctx context.Context, limit int, now time.Time) ([]Message, error) {
	defer db.timeOp("ListPendingOutgoingMessages", time.Now())
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.reader().QueryContext(ctx, `
		SELECT id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_kind, media_mime_type, media_local_path, media_thumbnail_local_path, media_width, media_height, media_animated, media_payload, media_cache_key,
		       reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction,
		       send_attempts, last_send_error, next_send_attempt, is_forwarded, mentioned_jids
		FROM messages
		WHERE direction = ? AND status = ? AND next_send_attempt <= ?
		ORDER BY sort_ms ASC, id ASC
		LIMIT ?
	`, DirectionOutgoing, StatusPending, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]Message, 0, limit)
	for rows.Next() {
		var message Message
		var mentionedRaw string
		if err := rows.Scan(
			&message.ID,
			&message.ChatID,
			&message.SenderID,
			&message.Text,
			&message.TimestampUnix,
			&message.Direction,
			&message.IsRead,
			&message.Status,
			&message.MediaKind,
			&message.MediaMimeType,
			&message.MediaLocalPath,
			&message.MediaThumbnailLocalPath,
			&message.MediaWidth,
			&message.MediaHeight,
			&message.MediaAnimated,
			&message.MediaPayload,
			&message.MediaCacheKey,
			&message.ReplyTo.MessageID,
			&message.ReplyTo.SenderID,
			&message.ReplyTo.SenderName,
			&message.ReplyTo.Text,
			&message.ReplyTo.MediaKind,
			&message.ReplyTo.MediaMimeType,
			&message.ReplyTo.Direction,
			&message.SendAttempts,
			&message.LastSendError,
			&message.NextSendAttempt,
			&message.IsForwarded,
			&mentionedRaw,
		); err != nil {
			return nil, err
		}
		message.Mentions = decodeMentions(mentionedRaw)
		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

func (db *DB) CountPendingOutgoingMessages(ctx context.Context) (int, error) {
	defer db.timeOp("CountPendingOutgoingMessages", time.Now())
	var count int
	err := db.reader().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM messages
		WHERE direction = ? AND status = ?
	`, DirectionOutgoing, StatusPending).Scan(&count)
	return count, err
}

// UpdateMessageSendAttempt records a transient send failure so the queue
// can skip the message until next_send_attempt.
func (db *DB) UpdateMessageSendAttempt(ctx context.Context, id string, attempts int32, sendErr string, nextAttempt time.Time) error {
	defer db.timeOp("UpdateMessageSendAttempt", time.Now())
	_, err := db.conn.ExecContext(ctx, `
		UPDATE messages
		SET send_attempts = ?, last_send_error = ?, next_send_attempt = ?
		WHERE id = ? AND status = ?
	`, attempts, sendErr, nextAttempt.Unix(), id, StatusPending)
	return err
}

func (db *DB) UpdateMessageStatus(ctx context.Context, id, status string) (Message, bool, error) {
	return db.updateMessageStatus(ctx, id, status, nextMessageStatus)
}

func (db *DB) UpdateMessageStatusFromHistory(ctx context.Context, id, status string) (Message, bool, error) {
	return db.updateMessageStatus(ctx, id, status, nextHistoryMessageStatus)
}

// UpdateMessagesStatus applies one receipt's status to every message it names,
// in a single transaction. A receipt can carry hundreds of ids and it arrives on
// whatsmeow's serialized handler queue, so a transaction each meant hundreds of
// commits on the one write connection with every other event waiting behind
// them. Ids with no row are skipped; the result holds only the messages that
// actually moved.
func (db *DB) UpdateMessagesStatus(ctx context.Context, ids []string, status string) ([]Message, error) {
	defer db.timeOp("UpdateMessagesStatus", time.Now())
	if len(ids) == 0 {
		return nil, nil
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	changed := make([]Message, 0, len(ids))
	for _, id := range ids {
		message, err := getMessageTx(ctx, tx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
			return nil, err
		}
		updatedStatus, moved := nextMessageStatus(message.Status, status)
		if !moved {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET status = ?
			WHERE id = ?
		`, updatedStatus, id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE chats
			SET last_message_status = ?
			WHERE id = ? AND last_message_time = ?
		`, updatedStatus, message.ChatID, message.TimestampUnix); err != nil {
			return nil, err
		}
		message.Status = updatedStatus
		changed = append(changed, message)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}

func (db *DB) updateMessageStatus(ctx context.Context, id, status string, nextStatus func(string, string) (string, bool)) (Message, bool, error) {
	defer db.timeOp("updateMessageStatus", time.Now())
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
		return Message{}, false, err
	}

	updatedStatus, changed := nextStatus(message.Status, status)
	if !changed {
		return message, false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET status = ?
		WHERE id = ?
	`, updatedStatus, id); err != nil {
		return Message{}, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE chats
		SET last_message_status = ?
		WHERE id = ? AND last_message_time = ?
	`, updatedStatus, message.ChatID, message.TimestampUnix); err != nil {
		return Message{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, false, err
	}

	message.Status = updatedStatus
	return message, true, nil
}

// MarkMessageRevoked tombstones a message that was deleted for everyone:
// the row survives (so ordering and reply previews keep working) but its
// content is cleared. Returns the refreshed message and chat, and whether
// anything changed (false when the message was already revoked).
func (db *DB) MarkMessageRevoked(ctx context.Context, id string, keepContent bool) (Message, Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	if message.IsRevoked {
		chat, err := getChatTx(ctx, tx, message.ChatID)
		if err != nil {
			return Message{}, Chat{}, false, err
		}
		return message, chat, false, nil
	}

	if keepContent {
		// Anti-delete: raise the flag but keep everything. The frontend
		// renders the original content with a Deleted mark.
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET is_revoked = 1 WHERE id = ?`, id); err != nil {
			return Message{}, Chat{}, false, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET is_revoked = 1,
			text = '',
			media_kind = '',
			media_mime_type = '',
			media_local_path = '',
			media_thumbnail_local_path = '',
			media_width = 0,
			media_height = 0,
			media_animated = 0,
			media_download_error = '',
			media_payload = x'',
			media_cache_key = '',
			media_duration_secs = 0,
			media_size_bytes = 0,
			media_file_name = '',
			media_page_count = 0,
			media_waveform = x'',
			media_played = 0,
			payload_json = '',
			payload_summary = '',
			reply_to_message_id = '',
			reply_to_sender_id = '',
			reply_to_sender_name = '',
			reply_to_text = '',
			reply_to_media_kind = '',
			reply_to_media_mime_type = '',
			reply_to_direction = ''
		WHERE id = ?
		`, id); err != nil {
			return Message{}, Chat{}, false, err
		}

		// A deleted-for-everyone message drops its reactions along with its content.
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_reactions WHERE message_id = ?`, id); err != nil {
			return Message{}, Chat{}, false, err
		}
	}

	// A revoked unread message no longer counts toward the badge.
	if message.Direction == DirectionIncoming && !message.IsRead {
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages SET is_read = 1 WHERE id = ?
		`, id); err != nil {
			return Message{}, Chat{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE chats SET unread_count = MAX(0, unread_count - 1) WHERE id = ?
		`, message.ChatID); err != nil {
			return Message{}, Chat{}, false, err
		}
	}

	if err := recomputeChatSummaryTx(ctx, tx, message.ChatID); err != nil {
		return Message{}, Chat{}, false, err
	}

	updated, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &updated); err != nil {
		return Message{}, Chat{}, false, err
	}
	chat, err := getChatTx(ctx, tx, message.ChatID)
	if err != nil {
		return Message{}, Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, Chat{}, false, err
	}
	return updated, chat, true, nil
}

// UpdateMessageText replaces a message's body (the caption for media messages)
// with newText and flags it as edited, then recomputes the chat summary so the
// chat list preview reflects the new content. It returns the reloaded message
// and chat plus whether anything changed. A revoked message cannot be edited,
// and re-applying the same text on an already-edited message is a no-op; both
// cases return changed=false.
// UpdateMessageText replaces a message's body in place (an edit). A nil
// mentions leaves the stored mentions untouched (used by our own edits, which
// don't re-author mentions); a non-nil slice — including empty — replaces them,
// so an incoming edit that dropped its @-mentions clears the column.
func (db *DB) UpdateMessageText(ctx context.Context, id, newText string, mentions []MessageMention) (Message, Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	if message.IsRevoked || (message.Text == newText && message.IsEdited) {
		chat, err := getChatTx(ctx, tx, message.ChatID)
		if err != nil {
			return Message{}, Chat{}, false, err
		}
		return message, chat, false, nil
	}

	// File the superseded body in the edit history before it is replaced.
	// Empty originals (e.g. a caption added later) carry no information.
	// The generated row id orders versions, so back-to-back edits in the
	// same millisecond both survive.
	if message.Text != "" && message.Text != newText {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_edits (message_id, edited_at_millis, text)
			VALUES (?, ?, ?)
		`, id, time.Now().UnixMilli(), message.Text); err != nil {
			return Message{}, Chat{}, false, err
		}
	}

	if mentions == nil {
		_, err = tx.ExecContext(ctx, `
			UPDATE messages
			SET text = ?,
				is_edited = 1
			WHERE id = ?
		`, newText, id)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE messages
			SET text = ?,
				is_edited = 1,
				mentioned_jids = ?
			WHERE id = ?
		`, newText, encodeMentions(mentions), id)
	}
	if err != nil {
		return Message{}, Chat{}, false, err
	}

	if err := recomputeChatSummaryTx(ctx, tx, message.ChatID); err != nil {
		return Message{}, Chat{}, false, err
	}

	updated, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &updated); err != nil {
		return Message{}, Chat{}, false, err
	}
	chat, err := getChatTx(ctx, tx, message.ChatID)
	if err != nil {
		return Message{}, Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, Chat{}, false, err
	}
	return updated, chat, true, nil
}

// MessageEdit is one superseded body version of an edited message.
// EditedAtMillis is a unix-millisecond timestamp (millis, not seconds, so
// two quick successive edits keep distinct, ordered rows).
type MessageEdit struct {
	MessageID      string
	EditedAtMillis int64
	Text           string
}

// ListMessageEdits returns a message's superseded bodies, oldest first. The
// live row itself holds the current version and is not included.
func (db *DB) ListMessageEdits(ctx context.Context, messageID string) ([]MessageEdit, error) {
	defer db.timeOp("ListMessageEdits", time.Now())
	rows, err := db.reader().QueryContext(ctx, `
		SELECT message_id, edited_at_millis, text
		FROM message_edits
		WHERE message_id = ?
		ORDER BY id ASC
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	edits := []MessageEdit{}
	for rows.Next() {
		var e MessageEdit
		if err := rows.Scan(&e.MessageID, &e.EditedAtMillis, &e.Text); err != nil {
			return nil, err
		}
		edits = append(edits, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return edits, nil
}

// StarredMessage pairs a starred message with its chat's display name, so the
// global starred view can label which conversation each message belongs to.
type StarredMessage struct {
	Message
	ChatName string
}

// SetMessageStarred flips a message's starred flag and returns the refreshed
// message plus whether the flag actually changed (re-applying the same state is
// a no-op, so callers can skip publishing).
func (db *DB) SetMessageStarred(ctx context.Context, id string, starred bool) (Message, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if message.IsStarred == starred {
		return message, false, nil
	}

	flag := 0
	if starred {
		flag = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET is_starred = ? WHERE id = ?`, flag, id); err != nil {
		return Message{}, false, err
	}

	updated, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &updated); err != nil {
		return Message{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, false, err
	}
	return updated, true, nil
}

// SetMessagePinned sets a message's pin window. pinnedUntil is the unix expiry
// (0 unpins); pinnedAt orders pins for banner navigation. Returns the refreshed
// message and whether anything changed.
func (db *DB) SetMessagePinned(ctx context.Context, id string, pinnedAt, pinnedUntil int64) (Message, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if message.PinnedUntil == pinnedUntil && message.PinnedAt == pinnedAt {
		return message, false, nil
	}

	if _, err := tx.ExecContext(ctx, `UPDATE messages SET pinned_at = ?, pinned_until = ? WHERE id = ?`, pinnedAt, pinnedUntil, id); err != nil {
		return Message{}, false, err
	}

	updated, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &updated); err != nil {
		return Message{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, false, err
	}
	return updated, true, nil
}

// SetMessageKept flips a message's kept flag: somebody asked for a
// disappearing message to stay. Returns the refreshed message and whether the
// flag actually changed, so a re-applied keep publishes nothing.
func (db *DB) SetMessageKept(ctx context.Context, id string, kept bool) (Message, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if message.IsKept == kept {
		return message, false, nil
	}

	flag := 0
	if kept {
		flag = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET is_kept = ? WHERE id = ?`, flag, id); err != nil {
		return Message{}, false, err
	}

	updated, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &updated); err != nil {
		return Message{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, false, err
	}
	return updated, true, nil
}

// CountActivePins returns how many messages in a chat are currently pinned
// (and unexpired), so callers can enforce the per-chat pin limit.
func (db *DB) CountActivePins(ctx context.Context, chatID string) (int, error) {
	var n int
	err := db.reader().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM messages WHERE chat_id = ? AND pinned_until > ?
	`, chatID, time.Now().Unix()).Scan(&n)
	return n, err
}

// ListPinnedMessages returns a chat's currently-pinned (unexpired) messages,
// oldest pin first, for the conversation's pinned-message banner.
func (db *DB) ListPinnedMessages(ctx context.Context, chatID string) ([]Message, error) {
	defer db.timeOp("ListPinnedMessages", time.Now())
	rows, err := db.reader().QueryContext(ctx, messageSelectPrefix+`
		WHERE m.chat_id = ? AND m.pinned_until > ?
		ORDER BY m.pinned_at ASC, m.sort_ms ASC, m.id ASC
	`, chatID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, 8)
	if err != nil {
		return nil, err
	}
	if err := db.attachMessageExtras(ctx, db.reader(), messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// GalleryMediaKinds are the kinds the per-chat media gallery shows: everything
// with a visual or playable body, in the order a switch should test them.
// Stickers and unsupported tombstones are deliberately absent.
var GalleryMediaKinds = []string{
	MediaKindImage,
	MediaKindVideo,
	MediaKindGIF,
	MediaKindVideoNote,
	MediaKindVoice,
	MediaKindAudio,
	MediaKindDocument,
}

// ListChatMediaMessages returns a chat's media messages newest first, for the
// media gallery. beforeMessageID is a keyset cursor for paging older results,
// matching ListStarredMessages. kinds narrows to a subset (empty means the
// whole gallery set).
func (db *DB) ListChatMediaMessages(ctx context.Context, chatID string, limit int, beforeMessageID string, kinds []string) ([]Message, error) {
	defer db.timeOp("ListChatMediaMessages", time.Now())
	if limit <= 0 {
		limit = 50
	}
	if len(kinds) == 0 {
		kinds = GalleryMediaKinds
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	query := messageSelectPrefix + `
		WHERE m.chat_id = ? AND m.is_revoked = 0 AND m.media_kind IN (` + placeholders + `)
	`
	args := []any{chatID}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	if beforeMessageID != "" {
		beforeSortMS, beforeID, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}
		query += ` AND (m.sort_ms < ? OR (m.sort_ms = ? AND m.id < ?))`
		args = append(args, beforeSortMS, beforeSortMS, beforeID)
	}
	query += `
		ORDER BY m.sort_ms DESC, m.id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
		return nil, err
	}
	if err := db.attachMessageExtras(ctx, db.reader(), messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// ListChatLinkMessages returns messages whose text contains a link, newest
// first; beforeMessageID is a keyset cursor for paging older results.
func (db *DB) ListChatLinkMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]Message, error) {
	defer db.timeOp("ListChatLinkMessages", time.Now())
	if limit <= 0 {
		limit = 50
	}

	query := messageSelectPrefix + `
		WHERE m.chat_id = ? AND m.is_revoked = 0
		AND (m.text LIKE '%http://%' ESCAPE '\' OR m.text LIKE '%https://%' ESCAPE '\' OR m.text LIKE '%www.%' ESCAPE '\')
	`
	args := []any{chatID}
	if beforeMessageID != "" {
		beforeSortMS, beforeID, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}
		query += ` AND (m.sort_ms < ? OR (m.sort_ms = ? AND m.id < ?))`
		args = append(args, beforeSortMS, beforeSortMS, beforeID)
	}
	query += `
		ORDER BY m.sort_ms DESC, m.id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
		return nil, err
	}
	if err := db.attachMessageExtras(ctx, db.reader(), messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// ListStarredMessages returns starred messages newest first. chatID == "" spans
// all chats; beforeMessageID is a keyset cursor for paging older results. Each
// row carries its chat's display name for the global view.
func (db *DB) ListStarredMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]StarredMessage, error) {
	defer db.timeOp("ListStarredMessages", time.Now())
	if limit <= 0 {
		limit = 50
	}

	query := messageSelectPrefix + `
		WHERE m.is_starred = 1
	`
	args := []any{}
	if chatID != "" {
		query += ` AND m.chat_id = ?`
		args = append(args, chatID)
	}
	if beforeMessageID != "" {
		beforeSortMS, beforeID, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}
		query += ` AND (m.sort_ms < ? OR (m.sort_ms = ? AND m.id < ?))`
		args = append(args, beforeSortMS, beforeSortMS, beforeID)
	}
	query += `
		ORDER BY m.sort_ms DESC, m.id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
		return nil, err
	}
	if err := db.attachMessageExtras(ctx, db.reader(), messages); err != nil {
		return nil, err
	}

	names, err := db.chatNamesByID(ctx, messages)
	if err != nil {
		return nil, err
	}
	result := make([]StarredMessage, len(messages))
	for i, m := range messages {
		result[i] = StarredMessage{Message: m, ChatName: names[m.ChatID]}
	}
	return result, nil
}

// MessageSearchResult is a full-text search hit: the matched message plus its
// chat's display name (for labeling cross-chat results in the global view).
type MessageSearchResult struct {
	Message
	ChatName string
}

// SearchMessages runs a full-text query against the messages_fts index, newest
// hit first. chatID == "" spans all chats; beforeMessageID is a keyset cursor
// for paging older results. The raw query is parsed into an FTS5 MATCH
// expression by ftsMatchQuery (prefix match on the final token). A
// blank/all-punctuation query returns no rows.
func (db *DB) SearchMessages(ctx context.Context, query, chatID string, limit int, beforeMessageID string) ([]MessageSearchResult, error) {
	defer db.timeOp("SearchMessages", time.Now())
	if limit <= 0 {
		limit = 50
	}

	match := ftsMatchQuery(query)
	if match == "" {
		return nil, nil
	}

	sql := messageSelectPrefix + `
		JOIN messages_fts f ON f.rowid = m.rowid
		WHERE messages_fts MATCH ?
	`
	args := []any{match}
	if chatID != "" {
		sql += ` AND m.chat_id = ?`
		args = append(args, chatID)
	}
	if beforeMessageID != "" {
		beforeSortMS, beforeID, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}
		sql += ` AND (m.sort_ms < ? OR (m.sort_ms = ? AND m.id < ?))`
		args = append(args, beforeSortMS, beforeSortMS, beforeID)
	}
	sql += `
		ORDER BY m.sort_ms DESC, m.id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.reader().QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
		return nil, err
	}
	if err := db.attachMessageExtras(ctx, db.reader(), messages); err != nil {
		return nil, err
	}

	names, err := db.chatNamesByID(ctx, messages)
	if err != nil {
		return nil, err
	}
	result := make([]MessageSearchResult, len(messages))
	for i, m := range messages {
		result[i] = MessageSearchResult{Message: m, ChatName: names[m.ChatID]}
	}
	return result, nil
}

// ftsMatchQuery turns free-form user input into a safe FTS5 MATCH expression.
// Each whitespace-separated token is wrapped in double quotes (so FTS5 treats
// it as a bare string and never as a column filter or operator), with embedded
// quotes doubled. The final token gets a trailing prefix marker so an
// in-progress word matches as the user types (e.g. `hello wor` -> `"hello" "wor"*`).
// Returns "" when the input yields no usable tokens.
func ftsMatchQuery(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for i, tok := range fields {
		quoted := `"` + strings.ReplaceAll(tok, `"`, `""`) + `"`
		if i == len(fields)-1 {
			quoted += "*"
		}
		parts = append(parts, quoted)
	}
	return strings.Join(parts, " ")
}

// chatNamesByID maps each message's chat_id to its chat display name in one
// query, used to label cross-chat starred results.
func (db *DB) chatNamesByID(ctx context.Context, messages []Message) (map[string]string, error) {
	names := make(map[string]string)
	if len(messages) == 0 {
		return names, nil
	}
	seen := make(map[string]struct{}, len(messages))
	ids := make([]any, 0, len(messages))
	placeholders := make([]string, 0, len(messages))
	for _, m := range messages {
		if _, ok := seen[m.ChatID]; ok {
			continue
		}
		seen[m.ChatID] = struct{}{}
		ids = append(ids, m.ChatID)
		placeholders = append(placeholders, "?")
	}
	rows, err := db.reader().QueryContext(ctx,
		`SELECT id, COALESCE(NULLIF(name, ''), '') FROM chats WHERE id IN (`+strings.Join(placeholders, ",")+`)`, ids...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}

// DeleteMessageForMe removes a local row and blocks later backfill
// returns the deleted message, refreshed chat, and whether a row existed
func (db *DB) DeleteMessageForMe(ctx context.Context, id string) (Message, Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	existed := true
	if errors.Is(err, sql.ErrNoRows) {
		existed = false
		message = Message{}
	} else if err != nil {
		return Message{}, Chat{}, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deleted_messages (id, chat_id, deleted_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, id, message.ChatID, time.Now().Unix()); err != nil {
		return Message{}, Chat{}, false, err
	}
	if !existed {
		if err := tx.Commit(); err != nil {
			return Message{}, Chat{}, false, err
		}
		return Message{}, Chat{}, false, nil
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id); err != nil {
		return Message{}, Chat{}, false, err
	}

	if message.Direction == DirectionIncoming && !message.IsRead {
		if _, err := tx.ExecContext(ctx, `
			UPDATE chats SET unread_count = MAX(0, unread_count - 1) WHERE id = ?
		`, message.ChatID); err != nil {
			return Message{}, Chat{}, false, err
		}
	}

	if err := recomputeChatSummaryTx(ctx, tx, message.ChatID); err != nil {
		return Message{}, Chat{}, false, err
	}

	chat, err := getChatTx(ctx, tx, message.ChatID)
	if err != nil {
		return Message{}, Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, Chat{}, false, err
	}
	return message, chat, true, nil
}

func (db *DB) UpdateMessageMediaLocalPath(ctx context.Context, id, localPath string) (Message, error) {
	return db.UpdateMessageMediaLocalPathWithDimensions(ctx, id, localPath, 0, 0)
}

// UpdateMessageMediaThumbnailLocalPath changes only the derived thumbnail
// path. It returns the complete message, including reactions, so publishing
// the resulting upsert cannot erase state held in related tables.
func (db *DB) UpdateMessageMediaThumbnailLocalPath(ctx context.Context, id, thumbnailPath string) (Message, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET media_thumbnail_local_path = ?
		WHERE id = ?
	`, thumbnailPath, id); err != nil {
		return Message{}, err
	}

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (db *DB) UpdateMessageMediaLocalPathWithDimensions(ctx context.Context, id, localPath string, mediaWidth, mediaHeight int32) (Message, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	if mediaWidth > 0 && mediaHeight > 0 {
		// Only fill dimensions in, never correct them. A renderer has already
		// reserved a slot from the sender-declared size, and rewriting it when
		// the bytes land makes every completed download reflow the timeline
		// under the reader. A sender that lies about its dimensions keeps its
		// wrong slot; a stable layout is worth more than a late correction.
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET media_local_path = ?,
			    media_width = CASE WHEN media_width > 0 THEN media_width ELSE ? END,
			    media_height = CASE WHEN media_height > 0 THEN media_height ELSE ? END,
			    media_download_error = ''
			WHERE id = ?
		`, localPath, mediaWidth, mediaHeight, id); err != nil {
			return Message{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET media_local_path = ?, media_download_error = ''
			WHERE id = ?
		`, localPath, id); err != nil {
			return Message{}, err
		}
	}

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
		return Message{}, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, err
	}

	return message, nil
}

func (db *DB) SetMessageMediaDownloadError(ctx context.Context, id, errorText string) (Message, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET media_download_error = ?
		WHERE id = ?
	`, errorText, id); err != nil {
		return Message{}, err
	}

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
		return Message{}, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// UpdateMessagePayload replaces a row's kind-specific payload and the one-line
// detail derived from it. Used by anything that moves after the message landed:
// a live share's position, a poll's question being edited.
func (db *DB) UpdateMessagePayload(ctx context.Context, id, payloadJSON, payloadSummary string) (Message, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE messages SET payload_json = ?, payload_summary = ? WHERE id = ?
	`, payloadJSON, payloadSummary, id); err != nil {
		return Message{}, err
	}
	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (db *DB) UpdateMessageMediaPayload(ctx context.Context, id string, payload []byte) (Message, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	if payload == nil {
		payload = []byte{}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET media_payload = ?
		WHERE id = ?
	`, payload, id); err != nil {
		return Message{}, err
	}

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
		return Message{}, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, err
	}

	return message, nil
}

// SetMessageMediaWaveform stores the amplitude buckets derived after download
// for a voice note whose sender omitted them.
func (db *DB) SetMessageMediaWaveform(ctx context.Context, id string, waveform []byte) (Message, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	if waveform == nil {
		waveform = []byte{}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET media_waveform = ?
		WHERE id = ?
	`, waveform, id); err != nil {
		return Message{}, err
	}

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
		return Message{}, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// MarkMessageMediaPlayed flips the played flag for an inbound voice note and
// reports whether this call was the one that flipped it, so the caller only
// sends a played receipt the first time.
func (db *DB) MarkMessageMediaPlayed(ctx context.Context, id string) (Message, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET media_played = 1
		WHERE id = ? AND media_played = 0
	`, id)
	if err != nil {
		return Message{}, false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Message{}, false, err
	}

	message, err := getMessageTx(ctx, tx, id)
	if err != nil {
		return Message{}, false, err
	}
	if err := db.attachMessageExtrasOne(ctx, tx, &message); err != nil {
		return Message{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, false, err
	}
	return message, rowsAffected > 0, nil
}

func (db *DB) DownloadedStickerPathByCacheKey(ctx context.Context, excludedMessageID, cacheKey string) (string, error) {
	if cacheKey == "" {
		return "", nil
	}
	var path string
	err := db.reader().QueryRowContext(ctx, `
		SELECT media_local_path
		FROM messages
		WHERE media_kind = ? AND media_local_path != '' AND media_cache_key = ? AND id != ?
		LIMIT 1
	`, MediaKindSticker, cacheKey, excludedMessageID).Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return path, err
}

func (db *DB) ListChatIDsBySenderID(ctx context.Context, senderID string) ([]string, error) {
	rows, err := db.reader().QueryContext(ctx, `
		SELECT DISTINCT chat_id FROM messages WHERE sender_id = ?
	`, senderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chatIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		chatIDs = append(chatIDs, id)
	}
	return chatIDs, rows.Err()
}

func (db *DB) ReadCandidatesForChat(ctx context.Context, chatID string) ([]ReadCandidate, error) {
	rows, err := db.reader().QueryContext(ctx, `
		SELECT id, chat_id, sender_id, timestamp, sort_ms
		FROM messages
		WHERE chat_id = ? AND direction = ? AND is_read = 0
		ORDER BY sort_ms ASC, id ASC
	`, chatID, DirectionIncoming)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]ReadCandidate, 0)
	for rows.Next() {
		var candidate ReadCandidate
		if err := rows.Scan(&candidate.InternalID, &candidate.ChatID, &candidate.SenderID, &candidate.TimestampUnix, &candidate.SortMS); err != nil {
			return nil, err
		}
		candidate.ExternalID = ExternalMessageID(chatID, candidate.InternalID)
		candidates = append(candidates, candidate)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return candidates, nil
}

func (db *DB) MarkMessagesRead(ctx context.Context, chatID string) (Chat, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET is_read = 1
		WHERE chat_id = ? AND direction = ? AND is_read = 0
	`, chatID, DirectionIncoming); err != nil {
		return Chat{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE chats
		SET unread_count = 0
		WHERE id = ?
	`, chatID); err != nil {
		return Chat{}, err
	}

	chat, err := getChatTx(ctx, tx, chatID)
	if err != nil {
		return Chat{}, err
	}

	if err := tx.Commit(); err != nil {
		return Chat{}, err
	}

	return chat, nil
}

// MarkChatReadUpTo marks incoming messages up to the given timestamp as read
// and recomputes the chat badge from what is actually still unread. The bound
// keeps a replayed (stale) mark-read from a linked device from clobbering
// messages that arrived after it. Returns sql.ErrNoRows for unknown chats:
// mark-read must never create a chat row.
func (db *DB) MarkChatReadUpTo(ctx context.Context, chatID string, uptoUnix int64) (Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, false, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET is_read = 1
		WHERE chat_id = ? AND direction = ? AND is_read = 0 AND timestamp <= ?
	`, chatID, DirectionIncoming, uptoUnix)
	if err != nil {
		return Chat{}, false, err
	}
	messagesChanged, err := result.RowsAffected()
	if err != nil {
		return Chat{}, false, err
	}

	chat, changed, err := recomputeChatUnreadTx(ctx, tx, chatID)
	if err != nil {
		return Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Chat{}, false, err
	}
	return chat, changed || messagesChanged > 0, nil
}

func (db *DB) MarkAllChatsRead(ctx context.Context) ([]string, error) {
	defer db.timeOp("MarkAllChatsRead", time.Now())
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT chat_id FROM messages
		WHERE direction = ? AND is_read = 0
	`, DirectionIncoming)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if _, err := tx.ExecContext(ctx, `
		UPDATE messages SET is_read = 1
		WHERE direction = ? AND is_read = 0
	`, DirectionIncoming); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET unread_count = 0 WHERE unread_count > 0`); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// MarkMessagesReadByIDs marks the given incoming messages as read (self read
// receipts name exact messages) and recomputes the chat badge. Returns
// sql.ErrNoRows for unknown chats.
//
// An empty id list still recomputes: a badge with no unread rows behind it is
// stale, and returning early would leave it permanently unclearable.
func (db *DB) MarkMessagesReadByIDs(ctx context.Context, chatID string, internalIDs []string) (Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, false, err
	}
	defer tx.Rollback()

	var messagesChanged int64
	if len(internalIDs) > 0 {
		args := make([]any, 0, len(internalIDs)+2)
		args = append(args, chatID, DirectionIncoming)
		for _, id := range internalIDs {
			args = append(args, id)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(internalIDs)), ",")
		result, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET is_read = 1
			WHERE chat_id = ? AND direction = ? AND is_read = 0 AND id IN (`+placeholders+`)
		`, args...)
		if err != nil {
			return Chat{}, false, err
		}
		if messagesChanged, err = result.RowsAffected(); err != nil {
			return Chat{}, false, err
		}
	}

	chat, changed, err := recomputeChatUnreadTx(ctx, tx, chatID)
	if err != nil {
		return Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Chat{}, false, err
	}
	return chat, changed || messagesChanged > 0, nil
}

// recomputeChatUnreadTx re-derives the chat badge from unread incoming message
// rows and reports whether the stored value moved. Fails with sql.ErrNoRows
// when the chat does not exist.
func recomputeChatUnreadTx(ctx context.Context, tx *sql.Tx, chatID string) (Chat, bool, error) {
	var before int32
	if err := tx.QueryRowContext(ctx, `SELECT unread_count FROM chats WHERE id = ?`, chatID).Scan(&before); err != nil {
		return Chat{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE chats
		SET unread_count = (
			SELECT COUNT(*)
			FROM messages
			WHERE chat_id = ? AND direction = ? AND is_read = 0
		)
		WHERE id = ?
	`, chatID, DirectionIncoming, chatID); err != nil {
		return Chat{}, false, err
	}
	chat, err := getChatTx(ctx, tx, chatID)
	if err != nil {
		return Chat{}, false, err
	}
	return chat, chat.UnreadCount != before, nil
}

func getChatRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (Chat, error) {
	var chat Chat
	var isGroup int
	var isPinned int
	var isFavorite int
	var isArchived int
	var isMuted int
	var historyExhausted int
	err := queryer.QueryRowContext(ctx, `
		SELECT c.id, c.name, c.name_source, c.last_message, c.last_message_time, c.last_message_direction, c.last_message_status, c.unread_count, c.is_group, c.is_pinned, c.pinned_order, c.is_favorite, c.updated_at, c.is_archived, c.is_muted, c.mute_end_timestamp, c.history_exhausted,
		       COALESCE(NULLIF(a.local_path, ''), c.avatar_local_path), COALESCE(NULLIF(a.picture_id, ''), c.avatar_picture_id), COALESCE(NULLIF(a.status, ''), c.avatar_status), COALESCE(NULLIF(a.checked_at, 0), c.avatar_checked_at)
		FROM chats c
		LEFT JOIN avatars a ON a.subject_kind = 'chat' AND a.subject_id = c.id
		WHERE c.id = ?
	`, id).Scan(
		&chat.ID,
		&chat.Name,
		&chat.NameSource,
		&chat.LastMessage,
		&chat.LastMessageTime,
		&chat.LastMessageDirection,
		&chat.LastMessageStatus,
		&chat.UnreadCount,
		&isGroup,
		&isPinned,
		&chat.PinnedOrder,
		&isFavorite,
		&chat.UpdatedAt,
		&isArchived,
		&isMuted,
		&chat.MuteEndTimestamp,
		&historyExhausted,
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
		&chat.AvatarStatus,
		&chat.AvatarCheckedAt,
	)
	chat.IsGroup = isGroup != 0
	chat.IsPinned = isPinned != 0
	chat.IsFavorite = isFavorite != 0
	chat.IsArchived = isArchived != 0
	chat.IsMuted = isMuted != 0
	chat.HistoryExhausted = historyExhausted != 0
	return chat, err
}

func getMessageRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, message *Message) error {
	var mentionedRaw string
	err := queryer.QueryRowContext(ctx, `
		SELECT m.id, m.chat_id, m.sender_id, m.sender_device,
		       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
		       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
		       m.text, m.timestamp, m.sort_ms, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated, m.media_download_error, m.media_payload, m.media_cache_key,
		       m.media_duration_secs, m.media_size_bytes, m.media_file_name, m.media_page_count, m.media_waveform, m.media_played,
		       m.payload_json, m.payload_summary, m.album_parent_id, m.album_index, m.is_kept, m.is_view_once,
		       m.reply_to_message_id, m.reply_to_sender_id, m.reply_to_sender_name, m.reply_to_text, m.reply_to_media_kind, m.reply_to_media_mime_type, m.reply_to_direction,
		       m.send_attempts, m.last_send_error, m.next_send_attempt, m.is_revoked, m.is_edited, m.is_starred, m.pinned_at, m.pinned_until, m.mentioned_jids
		FROM messages m
		LEFT JOIN senders s ON s.id = m.sender_id
		LEFT JOIN chats c ON c.id = m.sender_id
		LEFT JOIN avatars sa ON sa.subject_kind = 'sender' AND sa.subject_id = m.sender_id
		LEFT JOIN avatars ca ON ca.subject_kind = 'chat' AND ca.subject_id = m.sender_id
		WHERE m.id = ?
	`, id).Scan(
		&message.ID,
		&message.ChatID,
		&message.SenderID,
		&message.SenderDevice,
		&message.SenderName,
		&message.SenderAvatarLocalPath,
		&message.Text,
		&message.TimestampUnix,
		&message.SortMS,
		&message.Direction,
		&message.IsRead,
		&message.Status,
		&message.MediaKind,
		&message.MediaMimeType,
		&message.MediaLocalPath,
		&message.MediaThumbnailLocalPath,
		&message.MediaWidth,
		&message.MediaHeight,
		&message.MediaAnimated,
		&message.MediaDownloadError,
		&message.MediaPayload,
		&message.MediaCacheKey,
		&message.MediaDurationSecs,
		&message.MediaSizeBytes,
		&message.MediaFileName,
		&message.MediaPageCount,
		&message.MediaWaveform,
		&message.MediaPlayed,
		&message.PayloadJSON,
		&message.PayloadSummary,
		&message.AlbumParentID,
		&message.AlbumIndex,
		&message.IsKept,
		&message.IsViewOnce,
		&message.ReplyTo.MessageID,
		&message.ReplyTo.SenderID,
		&message.ReplyTo.SenderName,
		&message.ReplyTo.Text,
		&message.ReplyTo.MediaKind,
		&message.ReplyTo.MediaMimeType,
		&message.ReplyTo.Direction,
		&message.SendAttempts,
		&message.LastSendError,
		&message.NextSendAttempt,
		&message.IsRevoked,
		&message.IsEdited,
		&message.IsStarred,
		&message.PinnedAt,
		&message.PinnedUntil,
		&mentionedRaw,
	)
	message.Mentions = decodeMentions(mentionedRaw)
	return err
}

// SenderDisplay returns the stored display name and avatar path for a sender
// id; both empty (no error) when the sender is unknown.
func (db *DB) SenderDisplay(ctx context.Context, id string) (string, string, error) {
	var name, avatarLocalPath string
	err := db.reader().QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(s.name, ''), ''),
		       COALESCE(NULLIF(a.local_path, ''), NULLIF(s.avatar_local_path, ''), '')
		FROM senders s
		LEFT JOIN avatars a ON a.subject_kind = 'sender' AND a.subject_id = s.id
		WHERE s.id = ?
	`, id).Scan(&name, &avatarLocalPath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return name, avatarLocalPath, err
}

// SenderDisplays is the set-based form of SenderDisplay, for callers resolving
// a whole roster at once. A large group ran one query per candidate id per
// member — thousands of round trips through the reader pool for a single view
// open — where one IN() over the union answers all of them. Ids absent from
// `senders` are simply missing from the result, exactly as SenderDisplay
// reports them empty.
func (db *DB) SenderDisplays(ctx context.Context, ids []string) (map[string]SenderDisplayRow, error) {
	defer db.timeOp("SenderDisplays", time.Now())
	out := make(map[string]SenderDisplayRow, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// SQLITE_MAX_VARIABLE_NUMBER is 32766 on modern builds, but a roster can be
	// arbitrarily large; chunk so the statement is always well within it.
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, id := range batch {
			args = append(args, id)
		}
		rows, err := db.reader().QueryContext(ctx, `
			SELECT s.id,
			       COALESCE(NULLIF(s.name, ''), ''),
			       COALESCE(NULLIF(a.local_path, ''), NULLIF(s.avatar_local_path, ''), '')
			FROM senders s
			LEFT JOIN avatars a ON a.subject_kind = 'sender' AND a.subject_id = s.id
			WHERE s.id IN (`+placeholders+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var row SenderDisplayRow
			if err := rows.Scan(&id, &row.Name, &row.AvatarLocalPath); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = row
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SenderDisplayRow is one sender's presentation fields as SenderDisplays
// returns them.
type SenderDisplayRow struct {
	Name            string
	AvatarLocalPath string
}

func ExternalMessageID(chatID, internalID string) string {
	prefix := chatID + ":"
	if strings.HasPrefix(internalID, prefix) {
		return strings.TrimPrefix(internalID, prefix)
	}
	return internalID
}

func scanChat(scanner interface{ Scan(...any) error }) (Chat, error) {
	var chat Chat
	var isGroup int
	var isPinned int
	var isFavorite int
	var isArchived int
	var isMuted int
	var historyExhausted int
	err := scanner.Scan(
		&chat.ID,
		&chat.Name,
		&chat.NameSource,
		&chat.LastMessage,
		&chat.LastMessageTime,
		&chat.LastMessageDirection,
		&chat.LastMessageStatus,
		&chat.UnreadCount,
		&isGroup,
		&isPinned,
		&chat.PinnedOrder,
		&isFavorite,
		&chat.UpdatedAt,
		&isArchived,
		&isMuted,
		&chat.MuteEndTimestamp,
		&historyExhausted,
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
		&chat.AvatarStatus,
		&chat.AvatarCheckedAt,
	)
	chat.IsGroup = isGroup != 0
	chat.IsPinned = isPinned != 0
	chat.IsFavorite = isFavorite != 0
	chat.IsArchived = isArchived != 0
	chat.IsMuted = isMuted != 0
	chat.HistoryExhausted = historyExhausted != 0
	return chat, err
}

func reverseMessages(messages []Message) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}

func scanMessageRows(rows *sql.Rows, capacity int) ([]Message, error) {
	messages := make([]Message, 0, capacity)
	for rows.Next() {
		var message Message
		var mentionedRaw string
		if err := rows.Scan(
			&message.ID,
			&message.ChatID,
			&message.SenderID,
			&message.SenderDevice,
			&message.SenderName,
			&message.SenderAvatarLocalPath,
			&message.Text,
			&message.TimestampUnix,
			&message.SortMS,
			&message.Direction,
			&message.IsRead,
			&message.Status,
			&message.MediaKind,
			&message.MediaMimeType,
			&message.MediaLocalPath,
			&message.MediaThumbnailLocalPath,
			&message.MediaWidth,
			&message.MediaHeight,
			&message.MediaAnimated,
			&message.MediaDownloadError,
			&message.MediaDurationSecs,
			&message.MediaSizeBytes,
			&message.MediaFileName,
			&message.MediaPageCount,
			&message.MediaWaveform,
			&message.MediaPlayed,
			&message.PayloadJSON,
			&message.PayloadSummary,
			&message.AlbumParentID,
			&message.AlbumIndex,
			&message.IsKept,
			&message.IsViewOnce,
			&message.ReplyTo.MessageID,
			&message.ReplyTo.SenderID,
			&message.ReplyTo.SenderName,
			&message.ReplyTo.Text,
			&message.ReplyTo.MediaKind,
			&message.ReplyTo.MediaMimeType,
			&message.ReplyTo.Direction,
			&message.SendAttempts,
			&message.LastSendError,
			&message.NextSendAttempt,
			&message.IsRevoked,
			&message.IsEdited,
			&message.IsStarred,
			&message.PinnedAt,
			&message.PinnedUntil,
			&mentionedRaw,
		); err != nil {
			return nil, err
		}
		message.Mentions = decodeMentions(mentionedRaw)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func nextMessageStatus(current, incoming string) (string, bool) {
	if incoming == "" || incoming == current {
		return current, false
	}

	if incoming == StatusFailed {
		if current == StatusRead {
			return current, false
		}
		return incoming, true
	}

	if messageStatusRank(incoming) > messageStatusRank(current) {
		return incoming, true
	}

	return current, false
}

func nextHistoryMessageStatus(current, incoming string) (string, bool) {
	if current == StatusDelivered && incoming == StatusSent {
		return incoming, true
	}
	return nextMessageStatus(current, incoming)
}

func messageStatusRank(status string) int {
	switch status {
	case StatusPending:
		return 1
	case StatusSent:
		return 2
	case StatusDelivered:
		return 3
	case StatusRead:
		return 4
	case StatusFailed:
		return 5
	default:
		return 0
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
