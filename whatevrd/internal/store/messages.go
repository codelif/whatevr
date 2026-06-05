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

type Message struct {
	ID                      string
	ChatID                  string
	SenderID                string
	SenderName              string
	SenderAvatarLocalPath   string
	Text                    string
	TimestampUnix           int64
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
	SendAttempts            int32
	LastSendError           string
	NextSendAttempt         int64
	ReplyTo                 MessageReply
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
	ReplyTo        MessageReply
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

func (db *DB) SaveTextMessage(ctx context.Context, input TextMessageInput) (SavedTextMessage, error) {
	if input.ID == "" {
		return SavedTextMessage{}, errors.New("message id is required")
	}
	if input.ChatID == "" {
		return SavedTextMessage{}, errors.New("chat id is required")
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

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return SavedTextMessage{}, err
	}
	defer tx.Rollback()

	if err := upsertChat(ctx, tx, input); err != nil {
		return SavedTextMessage{}, err
	}
	if err := upsertSender(ctx, tx, input.SenderID, input.SenderName); err != nil {
		return SavedTextMessage{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, text, timestamp, direction, is_read, status, reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.ChatID, input.SenderID, input.Text, input.Timestamp.Unix(), input.Direction, boolToInt(!input.CountUnread), input.Status,
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
		unreadIncrement := 0
		if input.CountUnread {
			unreadIncrement = 1
		}
		nameSource := normalizeChatNameSource(input.ChatNameSource)

		if _, err := tx.ExecContext(ctx, `
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
		`, input.ChatName, nameSource, input.ChatName, input.ChatName, nameSource, nameSource, input.Timestamp.Unix(), input.Text, input.Timestamp.Unix(), input.Direction, input.Timestamp.Unix(), input.Status, input.Timestamp.Unix(), input.Timestamp.Unix(), unreadIncrement, boolToInt(input.IsGroup), input.ChatID); err != nil {
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

	if err := tx.Commit(); err != nil {
		return SavedTextMessage{}, err
	}

	return SavedTextMessage{Message: message, Chat: chat, Inserted: inserted}, nil
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

func (db *DB) SaveMediaMessage(ctx context.Context, input MediaMessageInput) (SavedTextMessage, error) {
	if input.ID == "" {
		return SavedTextMessage{}, errors.New("message id is required")
	}
	if input.ChatID == "" {
		return SavedTextMessage{}, errors.New("chat id is required")
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
	if input.MediaPayload == nil {
		input.MediaPayload = []byte{}
	}

	lastMessage := messageSummary(input.Text, input.MediaKind, input.MediaMimeType)

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return SavedTextMessage{}, err
	}
	defer tx.Rollback()

	if err := upsertChat(ctx, tx, input.TextMessageInput); err != nil {
		return SavedTextMessage{}, err
	}
	if err := upsertSender(ctx, tx, input.SenderID, input.SenderName); err != nil {
		return SavedTextMessage{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_kind, media_mime_type, media_local_path, media_thumbnail_local_path, media_width, media_height, media_animated, media_payload, media_cache_key, reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.ChatID, input.SenderID, input.Text, input.Timestamp.Unix(), input.Direction,
		boolToInt(!input.CountUnread), input.Status, input.MediaKind, input.MediaMimeType, input.MediaLocalPath, input.MediaThumbnailLocalPath, input.MediaWidth, input.MediaHeight, boolToInt(input.MediaAnimated), input.MediaPayload, input.MediaCacheKey,
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
		unreadIncrement := 0
		if input.CountUnread {
			unreadIncrement = 1
		}
		nameSource := normalizeChatNameSource(input.ChatNameSource)

		if _, err := tx.ExecContext(ctx, `
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
		`, input.ChatName, nameSource, input.ChatName, input.ChatName, nameSource, nameSource, input.Timestamp.Unix(), lastMessage, input.Timestamp.Unix(), input.Direction, input.Timestamp.Unix(), input.Status, input.Timestamp.Unix(), input.Timestamp.Unix(), unreadIncrement, boolToInt(input.IsGroup), input.ChatID); err != nil {
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

	if err := tx.Commit(); err != nil {
		return SavedTextMessage{}, err
	}

	return SavedTextMessage{Message: message, Chat: chat, Inserted: inserted}, nil
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
	err := db.conn.QueryRowContext(ctx, `
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
					WHEN text != '' THEN text
					WHEN media_kind = 'sticker' THEN '[Sticker]'
					WHEN media_mime_type IN ('image/jpeg', 'image/png', 'image/webp', 'image/gif') THEN '[Image]'
					WHEN media_mime_type IN ('video/mp4', 'video/webm') THEN '[Video]'
					WHEN media_mime_type IN ('audio/ogg', 'audio/mpeg') THEN '[Audio]'
					ELSE '[Media]'
				END
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, id DESC
				LIMIT 1
			), ''),
			last_message_time = COALESCE((
				SELECT timestamp
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, id DESC
				LIMIT 1
			), 0),
			last_message_direction = COALESCE((
				SELECT direction
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, id DESC
				LIMIT 1
			), ''),
			last_message_status = COALESCE((
				SELECT status
				FROM messages
				WHERE chat_id = ?
				ORDER BY timestamp DESC, id DESC
				LIMIT 1
			), '')
		WHERE id = ?
	`, chatID, chatID, chatID, chatID, chatID)
	return err
}

func (db *DB) ListMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT m.id, m.chat_id, m.sender_id,
		       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
		       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
		       m.text, m.timestamp, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated,
		       m.reply_to_message_id, m.reply_to_sender_id, m.reply_to_sender_name, m.reply_to_text, m.reply_to_media_kind, m.reply_to_media_mime_type, m.reply_to_direction,
		       m.send_attempts, m.last_send_error, m.next_send_attempt
		FROM messages m
		LEFT JOIN senders s ON s.id = m.sender_id
		LEFT JOIN chats c ON c.id = m.sender_id
		LEFT JOIN avatars sa ON sa.subject_kind = 'sender' AND sa.subject_id = m.sender_id
		LEFT JOIN avatars ca ON ca.subject_kind = 'chat' AND ca.subject_id = m.sender_id
		WHERE m.chat_id = ?
	`
	args := []any{chatID}

	if beforeMessageID != "" {
		beforeTimestamp, beforeID, err := db.messageCursor(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}

		query += `
			AND (m.timestamp < ? OR (m.timestamp = ? AND m.id < ?))
		`
		args = append(args, beforeTimestamp, beforeTimestamp, beforeID)
	}

	query += `
		ORDER BY m.timestamp DESC, m.id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages, err := scanMessageRows(rows, limit)
	if err != nil {
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
	olderDesc, err := db.listMessagesAroundSide(ctx, chatID, target.TimestampUnix, target.ID, capacity, false)
	if err != nil {
		return nil, err
	}
	newerAsc, err := db.listMessagesAroundSide(ctx, chatID, target.TimestampUnix, target.ID, capacity, true)
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

func (db *DB) listMessagesAroundSide(ctx context.Context, chatID string, timestamp int64, messageID string, limit int, newer bool) ([]Message, error) {
	if limit <= 0 {
		return nil, nil
	}

	comparison := `AND (m.timestamp < ? OR (m.timestamp = ? AND m.id < ?))`
	order := `ORDER BY m.timestamp DESC, m.id DESC`
	if newer {
		comparison = `AND (m.timestamp > ? OR (m.timestamp = ? AND m.id > ?))`
		order = `ORDER BY m.timestamp ASC, m.id ASC`
	}

	query := `
		SELECT m.id, m.chat_id, m.sender_id,
		       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
		       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
		       m.text, m.timestamp, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated,
		       m.reply_to_message_id, m.reply_to_sender_id, m.reply_to_sender_name, m.reply_to_text, m.reply_to_media_kind, m.reply_to_media_mime_type, m.reply_to_direction,
		       m.send_attempts, m.last_send_error, m.next_send_attempt
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

	rows, err := db.conn.QueryContext(ctx, query, chatID, timestamp, timestamp, messageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMessageRows(rows, limit)
}

func (db *DB) messageCursor(ctx context.Context, id string) (int64, string, error) {
	var timestamp int64
	var messageID string
	err := db.conn.QueryRowContext(ctx, `SELECT timestamp, id FROM messages WHERE id = ?`, id).Scan(&timestamp, &messageID)
	return timestamp, messageID, err
}

func (db *DB) GetMessage(ctx context.Context, id string) (Message, error) {
	var message Message
	err := getMessageRow(ctx, db.conn, id, &message)
	return message, err
}

func (db *DB) ListPendingOutgoingMessages(ctx context.Context, limit int, now time.Time) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_kind, media_mime_type, media_local_path, media_thumbnail_local_path, media_width, media_height, media_animated, media_payload,
		       reply_to_message_id, reply_to_sender_id, reply_to_sender_name, reply_to_text, reply_to_media_kind, reply_to_media_mime_type, reply_to_direction,
		       send_attempts, last_send_error, next_send_attempt
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
		); err != nil {
			return nil, err
		}
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

func (db *DB) ListDownloadedStickerMessages(ctx context.Context) ([]Message, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_kind, media_mime_type, media_local_path, media_thumbnail_local_path, media_width, media_height, media_animated, media_payload, media_cache_key,
		       send_attempts, last_send_error, next_send_attempt
		FROM messages
		WHERE media_kind = ? AND media_local_path != ''
	`, MediaKindSticker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var message Message
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
			&message.SendAttempts,
			&message.LastSendError,
			&message.NextSendAttempt,
		); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (db *DB) DownloadedStickerPathByCacheKey(ctx context.Context, excludedMessageID, cacheKey string) (string, error) {
	if cacheKey == "" {
		return "", nil
	}
	var path string
	err := db.conn.QueryRowContext(ctx, `
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
	rows, err := db.conn.QueryContext(ctx, `
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
	rows, err := db.conn.QueryContext(ctx, `
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
	err := queryer.QueryRowContext(ctx, `
		SELECT c.id, c.name, c.name_source, c.last_message, c.last_message_time, c.last_message_direction, c.last_message_status, c.unread_count, c.is_group, c.is_pinned, c.pinned_order,
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
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
		&chat.AvatarStatus,
		&chat.AvatarCheckedAt,
	)
	chat.IsGroup = isGroup != 0
	chat.IsPinned = isPinned != 0
	return chat, err
}

func getMessageRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, message *Message) error {
	return queryer.QueryRowContext(ctx, `
		SELECT m.id, m.chat_id, m.sender_id,
		       COALESCE(NULLIF(s.name, ''), NULLIF(c.name, ''), ''),
		       COALESCE(NULLIF(sa.local_path, ''), NULLIF(ca.local_path, ''), NULLIF(s.avatar_local_path, ''), NULLIF(c.avatar_local_path, ''), ''),
		       m.text, m.timestamp, m.direction, m.is_read, m.status, m.media_kind, m.media_mime_type, m.media_local_path, m.media_thumbnail_local_path, m.media_width, m.media_height, m.media_animated, m.media_payload, m.media_cache_key,
		       m.reply_to_message_id, m.reply_to_sender_id, m.reply_to_sender_name, m.reply_to_text, m.reply_to_media_kind, m.reply_to_media_mime_type, m.reply_to_direction,
		       m.send_attempts, m.last_send_error, m.next_send_attempt
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
	)
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
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
		&chat.AvatarStatus,
		&chat.AvatarCheckedAt,
	)
	chat.IsGroup = isGroup != 0
	chat.IsPinned = isPinned != 0
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
		if err := rows.Scan(
			&message.ID,
			&message.ChatID,
			&message.SenderID,
			&message.SenderName,
			&message.SenderAvatarLocalPath,
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
		); err != nil {
			return nil, err
		}
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
