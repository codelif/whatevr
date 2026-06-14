package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	DirectionIncoming = "incoming"
	DirectionOutgoing = "outgoing"

	MediaKindImage   = "image"
	MediaKindSticker = "sticker"

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
	ID                      string
	ChatID                  string
	SenderID                string
	SenderName              string
	SenderAvatarLocalPath   string
	Text                    string
	TimestampUnix           int64
	SortSeq                 int64
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
	MediaPayload            []byte
	MediaCacheKey           string
	IsRevoked               bool
	IsForwarded             bool
	IsEdited                bool
	IsStarred               bool
	PinnedAt                int64
	PinnedUntil             int64
	SendAttempts            int32
	LastSendError           string
	NextSendAttempt         int64
	ReplyTo                 MessageReply
	Reactions               []Reaction
	// @-mentioned participants with names resolved at ingest. Empty for the
	// vast majority of messages.
	Mentions []MessageMention
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
}

type ReadCandidate struct {
	InternalID    string
	ExternalID    string
	ChatID        string
	SenderID      string
	TimestampUnix int64
}

type TextMessageInput struct {
	ID             string
	ChatID         string
	ChatName       string
	ChatNameSource string
	SenderID       string
	SenderName     string
	Text           string
	Timestamp      time.Time
	Direction      string
	Status         string
	IsGroup        bool
	CountUnread    bool
	IsForwarded    bool
	ReplyTo        MessageReply
	Mentions       []MessageMention
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
func saveTextMessageTx(ctx context.Context, tx *sql.Tx, input TextMessageInput) (SavedTextMessage, error) {
	if err := upsertChat(ctx, tx, input); err != nil {
		return SavedTextMessage{}, err
	}
	if err := upsertSender(ctx, tx, input.SenderID, input.SenderName); err != nil {
		return SavedTextMessage{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, text, timestamp, direction, is_read, status, is_forwarded, mentioned_jids, reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.ChatID, input.SenderID, input.Text, input.Timestamp.Unix(), input.Direction, boolToInt(!input.CountUnread), input.Status, boolToInt(input.IsForwarded), encodeMentions(input.Mentions),
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
		if err := bumpChatForInsertedMessage(ctx, tx, input, input.Text); err != nil {
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
	lastMessage := messageSummary(input.Text, input.MediaKind, input.MediaMimeType)

	if err := upsertChat(ctx, tx, input.TextMessageInput); err != nil {
		return SavedTextMessage{}, err
	}
	if err := upsertSender(ctx, tx, input.SenderID, input.SenderName); err != nil {
		return SavedTextMessage{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, text, timestamp, direction, is_read, status, is_forwarded, mentioned_jids, media_kind, media_mime_type, media_local_path, media_thumbnail_local_path, media_width, media_height, media_animated, media_payload, media_cache_key, reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.ChatID, input.SenderID, input.Text, input.Timestamp.Unix(), input.Direction,
		boolToInt(!input.CountUnread), input.Status, boolToInt(input.IsForwarded), encodeMentions(input.Mentions), input.MediaKind, input.MediaMimeType, input.MediaLocalPath, input.MediaThumbnailLocalPath, input.MediaWidth, input.MediaHeight, boolToInt(input.MediaAnimated), input.MediaPayload, input.MediaCacheKey,
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
		if err := bumpChatForInsertedMessage(ctx, tx, input.TextMessageInput, lastMessage); err != nil {
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

func messageSummary(text, mediaKind, mediaMimeType string) string {
	if text != "" {
		return text
	}
	switch {
	case mediaKind == MediaKindSticker:
		return "[Sticker]"
	case mediaMimeType == "image/jpeg" || mediaMimeType == "image/png" || mediaMimeType == "image/webp" || mediaMimeType == "image/gif":
		return "[Image]"
	case mediaMimeType == "video/mp4" || mediaMimeType == "video/webm":
		return "[Video]"
	case mediaMimeType == "audio/ogg" || mediaMimeType == "audio/mpeg":
		return "[Audio]"
	default:
		return "[Media]"
	}
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

	result, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET timestamp = ?
		WHERE id = ? AND timestamp > ?
	`, effectiveTimestampUnix, id, effectiveTimestampUnix)
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

func recomputeChatSummaryTx(ctx context.Context, tx *sql.Tx, chatID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE chats
		SET last_message = COALESCE((
				SELECT CASE
					WHEN is_revoked = 1 THEN 'This message was deleted'
					WHEN text != '' THEN text
					WHEN media_kind = 'sticker' THEN '[Sticker]'
					WHEN media_mime_type IN ('image/jpeg', 'image/png', 'image/webp', 'image/gif') THEN '[Image]'
					WHEN media_mime_type IN ('video/mp4', 'video/webm') THEN '[Video]'
					WHEN media_mime_type IN ('audio/ogg', 'audio/mpeg') THEN '[Audio]'
					ELSE '[Media]'
				END
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, rowid DESC
				LIMIT 1
			), ''),
			last_message_time = COALESCE((
				SELECT timestamp
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, rowid DESC
				LIMIT 1
			), 0),
			last_message_direction = COALESCE((
				SELECT direction
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, rowid DESC
				LIMIT 1
			), ''),
			last_message_status = COALESCE((
				SELECT status
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, rowid DESC
				LIMIT 1
			), '')
		WHERE id = ?
	`, chatID, chatID, chatID, chatID, chatID)
	return err
}

// messageSelectPrefix is the SELECT + JOIN block shared by message list
// queries. Its column order matches scanMessageRows exactly; append a WHERE /
// ORDER BY / LIMIT clause to use it.
const messageSelectPrefix = `
	SELECT m.id, m.chat_id, m.sender_id,
	       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
	       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
	       m.text, m.timestamp, m.rowid, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated,
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

	query := `
		SELECT m.id, m.chat_id, m.sender_id,
		       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
		       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
		       m.text, m.timestamp, m.rowid, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated,
		       m.reply_to_message_id, m.reply_to_sender_id, m.reply_to_sender_name, m.reply_to_text, m.reply_to_media_kind, m.reply_to_media_mime_type, m.reply_to_direction,
		       m.send_attempts, m.last_send_error, m.next_send_attempt, m.is_revoked, m.is_edited, m.is_starred, m.pinned_at, m.pinned_until, m.mentioned_jids
		FROM messages m
		LEFT JOIN senders s ON s.id = m.sender_id
		LEFT JOIN chats c ON c.id = m.sender_id
		LEFT JOIN avatars sa ON sa.subject_kind = 'sender' AND sa.subject_id = m.sender_id
		LEFT JOIN avatars ca ON ca.subject_kind = 'chat' AND ca.subject_id = m.sender_id
		WHERE m.chat_id = ?
	`
	args := []any{chatID}

	if beforeMessageID != "" {
		beforeTimestamp, beforeSeq, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}

		query += `
			AND (m.timestamp < ? OR (m.timestamp = ? AND m.rowid < ?))
		`
		args = append(args, beforeTimestamp, beforeTimestamp, beforeSeq)
	}

	query += `
		ORDER BY m.timestamp DESC, m.rowid DESC
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
	if err := attachReactions(ctx, db.reader(), messages); err != nil {
		return nil, err
	}

	reverseMessages(messages)
	return messages, nil
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
	if limit == 1 {
		return []Message{target}, nil
	}

	capacity := limit - 1
	olderDesc, err := db.listMessagesAroundSide(ctx, chatID, target.TimestampUnix, target.SortSeq, capacity, false)
	if err != nil {
		return nil, err
	}
	newerAsc, err := db.listMessagesAroundSide(ctx, chatID, target.TimestampUnix, target.SortSeq, capacity, true)
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

func (db *DB) listMessagesAroundSide(ctx context.Context, chatID string, timestamp int64, seq int64, limit int, newer bool) ([]Message, error) {
	if limit <= 0 {
		return nil, nil
	}

	comparison := `AND (m.timestamp < ? OR (m.timestamp = ? AND m.rowid < ?))`
	order := `ORDER BY m.timestamp DESC, m.rowid DESC`
	if newer {
		comparison = `AND (m.timestamp > ? OR (m.timestamp = ? AND m.rowid > ?))`
		order = `ORDER BY m.timestamp ASC, m.rowid ASC`
	}

	query := `
		SELECT m.id, m.chat_id, m.sender_id,
		       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
		       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
		       m.text, m.timestamp, m.rowid, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated,
		       m.reply_to_message_id, m.reply_to_sender_id, m.reply_to_sender_name, m.reply_to_text, m.reply_to_media_kind, m.reply_to_media_mime_type, m.reply_to_direction,
		       m.send_attempts, m.last_send_error, m.next_send_attempt, m.is_revoked, m.is_edited, m.is_starred, m.pinned_at, m.pinned_until, m.mentioned_jids
		FROM messages m
		LEFT JOIN senders s ON s.id = m.sender_id
		LEFT JOIN chats c ON c.id = m.sender_id
		LEFT JOIN avatars sa ON sa.subject_kind = 'sender' AND sa.subject_id = m.sender_id
		LEFT JOIN avatars ca ON ca.subject_kind = 'chat' AND ca.subject_id = m.sender_id
		WHERE m.chat_id = ?
	` + comparison + `
		` + order + `
		LIMIT ?
	`

	rows, err := db.reader().QueryContext(ctx, query, chatID, timestamp, timestamp, seq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
		return nil, err
	}
	if err := attachReactions(ctx, db.reader(), messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (db *DB) messageCursor(ctx context.Context, id string) (int64, int64, error) {
	var timestamp int64
	var seq int64
	err := db.reader().QueryRowContext(ctx, `SELECT timestamp, rowid FROM messages WHERE id = ?`, id).Scan(&timestamp, &seq)
	return timestamp, seq, err
}

func (db *DB) GetMessage(ctx context.Context, id string) (Message, error) {
	defer db.timeOp("GetMessage", time.Now())
	var message Message
	if err := getMessageRow(ctx, db.reader(), id, &message); err != nil {
		return message, err
	}
	if err := attachReactionsOne(ctx, db.reader(), &message); err != nil {
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
		ORDER BY timestamp ASC, rowid ASC
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
func (db *DB) MarkMessageRevoked(ctx context.Context, id string) (Message, Chat, bool, error) {
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
			media_payload = x'',
			media_cache_key = ''
		WHERE id = ?
	`, id); err != nil {
		return Message{}, Chat{}, false, err
	}

	// A deleted-for-everyone message drops its reactions along with its content.
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_reactions WHERE message_id = ?`, id); err != nil {
		return Message{}, Chat{}, false, err
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
	chat, err := getChatTx(ctx, tx, message.ChatID)
	if err != nil {
		return Message{}, Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Message{}, Chat{}, false, err
	}
	return updated, chat, true, nil
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
		ORDER BY m.pinned_at ASC, m.rowid ASC
	`, chatID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, 8)
	if err != nil {
		return nil, err
	}
	if err := attachReactions(ctx, db.reader(), messages); err != nil {
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
		beforeTimestamp, beforeSeq, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}
		query += ` AND (m.timestamp < ? OR (m.timestamp = ? AND m.rowid < ?))`
		args = append(args, beforeTimestamp, beforeTimestamp, beforeSeq)
	}
	query += `
		ORDER BY m.timestamp DESC, m.rowid DESC
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
	if err := attachReactions(ctx, db.reader(), messages); err != nil {
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
		beforeTimestamp, beforeSeq, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}
		sql += ` AND (m.timestamp < ? OR (m.timestamp = ? AND m.rowid < ?))`
		args = append(args, beforeTimestamp, beforeTimestamp, beforeSeq)
	}
	sql += `
		ORDER BY m.timestamp DESC, m.rowid DESC
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
	if err := attachReactions(ctx, db.reader(), messages); err != nil {
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

// DeleteMessageForMe removes a message row locally (receipts cascade via FK).
// Returns the deleted message, the refreshed chat, and whether a row existed.
func (db *DB) DeleteMessageForMe(ctx context.Context, id string) (Message, Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, Chat{}, false, nil
	}
	if err != nil {
		return Message{}, Chat{}, false, err
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

func (db *DB) UpdateMessageMediaLocalPathWithDimensions(ctx context.Context, id, localPath string, mediaWidth, mediaHeight int32) (Message, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	if mediaWidth > 0 && mediaHeight > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET media_local_path = ?, media_width = ?, media_height = ?
			WHERE id = ?
		`, localPath, mediaWidth, mediaHeight, id); err != nil {
			return Message{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET media_local_path = ?
			WHERE id = ?
		`, localPath, id); err != nil {
			return Message{}, err
		}
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

	if err := tx.Commit(); err != nil {
		return Message{}, err
	}

	return message, nil
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
		SELECT id, chat_id, sender_id, timestamp
		FROM messages
		WHERE chat_id = ? AND direction = ? AND is_read = 0
		ORDER BY timestamp ASC, rowid ASC
	`, chatID, DirectionIncoming)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]ReadCandidate, 0)
	for rows.Next() {
		var candidate ReadCandidate
		if err := rows.Scan(&candidate.InternalID, &candidate.ChatID, &candidate.SenderID, &candidate.TimestampUnix); err != nil {
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

func getChatRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (Chat, error) {
	var chat Chat
	var isGroup int
	var isPinned int
	var isArchived int
	var isMuted int
	err := queryer.QueryRowContext(ctx, `
		SELECT c.id, c.name, c.name_source, c.last_message, c.last_message_time, c.last_message_direction, c.last_message_status, c.unread_count, c.is_group, c.is_pinned, c.pinned_order, c.updated_at, c.is_archived, c.is_muted, c.mute_end_timestamp,
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
		&chat.UpdatedAt,
		&isArchived,
		&isMuted,
		&chat.MuteEndTimestamp,
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
		&chat.AvatarStatus,
		&chat.AvatarCheckedAt,
	)
	chat.IsGroup = isGroup != 0
	chat.IsPinned = isPinned != 0
	chat.IsArchived = isArchived != 0
	chat.IsMuted = isMuted != 0
	return chat, err
}

func getMessageRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, message *Message) error {
	var mentionedRaw string
	err := queryer.QueryRowContext(ctx, `
		SELECT m.id, m.chat_id, m.sender_id,
		       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
		       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
		       m.text, m.timestamp, m.rowid, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated, m.media_payload, m.media_cache_key,
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
		&message.SenderName,
		&message.SenderAvatarLocalPath,
		&message.Text,
		&message.TimestampUnix,
		&message.SortSeq,
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
	var isArchived int
	var isMuted int
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
		&chat.UpdatedAt,
		&isArchived,
		&isMuted,
		&chat.MuteEndTimestamp,
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
		&chat.AvatarStatus,
		&chat.AvatarCheckedAt,
	)
	chat.IsGroup = isGroup != 0
	chat.IsPinned = isPinned != 0
	chat.IsArchived = isArchived != 0
	chat.IsMuted = isMuted != 0
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
			&message.SenderName,
			&message.SenderAvatarLocalPath,
			&message.Text,
			&message.TimestampUnix,
			&message.SortSeq,
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
