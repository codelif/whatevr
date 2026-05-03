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

	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusRead      = "read"
	StatusFailed    = "failed"
	StatusSent      = "sent"
)

type Message struct {
	ID             string
	ChatID         string
	SenderID       string
	Text           string
	TimestampUnix  int64
	Direction      string
	IsRead         bool
	Status         string
	MediaMimeType  string
	MediaLocalPath string
	MediaWidth     int32
	MediaHeight    int32
}

type MediaMessageInput struct {
	TextMessageInput
	MediaMimeType  string
	MediaLocalPath string
	MediaWidth     int32
	MediaHeight    int32
}

type ReadCandidate struct {
	InternalID    string
	ExternalID    string
	ChatID        string
	SenderID      string
	TimestampUnix int64
}

type TextMessageInput struct {
	ID          string
	ChatID      string
	ChatName    string
	SenderID    string
	Text        string
	Timestamp   time.Time
	Direction   string
	Status      string
	IsGroup     bool
	CountUnread bool
}

type SavedTextMessage struct {
	Message  Message
	Chat     Chat
	Inserted bool
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

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, text, timestamp, direction, is_read, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.ChatID, input.SenderID, input.Text, input.Timestamp.Unix(), input.Direction, boolToInt(!input.CountUnread), input.Status)
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

		if _, err := tx.ExecContext(ctx, `
			UPDATE chats
			SET name = CASE WHEN ? != '' THEN ? ELSE name END,
				last_message = CASE WHEN ? >= last_message_time THEN ? ELSE last_message END,
				last_message_time = CASE WHEN ? >= last_message_time THEN ? ELSE last_message_time END,
				unread_count = unread_count + ?,
				is_group = ?
			WHERE id = ?
		`, input.ChatName, input.ChatName, input.Timestamp.Unix(), input.Text, input.Timestamp.Unix(), input.Timestamp.Unix(), unreadIncrement, boolToInt(input.IsGroup), input.ChatID); err != nil {
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
	if insertName == "" {
		insertName = input.ChatID
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO chats (id, name, last_message, last_message_time, unread_count, is_group)
		VALUES (?, ?, '', 0, 0, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = CASE WHEN ? != '' THEN ? ELSE chats.name END,
			is_group = excluded.is_group
	`, input.ChatID, insertName, boolToInt(input.IsGroup), input.ChatName, input.ChatName)
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
	if input.Timestamp.IsZero() {
		input.Timestamp = time.Now()
	}
	if input.Direction == "" {
		input.Direction = DirectionIncoming
	}
	if input.Status == "" {
		input.Status = StatusDelivered
	}

	lastMessage := input.Text
	if lastMessage == "" {
		switch {
		case input.MediaMimeType == "image/jpeg" || input.MediaMimeType == "image/png" || input.MediaMimeType == "image/webp":
			lastMessage = "[Image]"
		case input.MediaMimeType == "video/mp4" || input.MediaMimeType == "video/webm":
			lastMessage = "[Video]"
		case input.MediaMimeType == "audio/ogg" || input.MediaMimeType == "audio/mpeg":
			lastMessage = "[Audio]"
		default:
			lastMessage = "[Media]"
		}
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return SavedTextMessage{}, err
	}
	defer tx.Rollback()

	if err := upsertChat(ctx, tx, input.TextMessageInput); err != nil {
		return SavedTextMessage{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_mime_type, media_local_path, media_width, media_height)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.ChatID, input.SenderID, input.Text, input.Timestamp.Unix(), input.Direction,
		boolToInt(!input.CountUnread), input.Status, input.MediaMimeType, input.MediaLocalPath, input.MediaWidth, input.MediaHeight)
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

		if _, err := tx.ExecContext(ctx, `
			UPDATE chats
			SET name = CASE WHEN ? != '' THEN ? ELSE name END,
				last_message = CASE WHEN ? >= last_message_time THEN ? ELSE last_message END,
				last_message_time = CASE WHEN ? >= last_message_time THEN ? ELSE last_message_time END,
				unread_count = unread_count + ?,
				is_group = ?
			WHERE id = ?
		`, input.ChatName, input.ChatName, input.Timestamp.Unix(), lastMessage, input.Timestamp.Unix(), input.Timestamp.Unix(), unreadIncrement, boolToInt(input.IsGroup), input.ChatID); err != nil {
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

func (db *DB) ListMessages(ctx context.Context, chatID string, limit int, beforeMessageID string) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_mime_type, media_local_path, media_width, media_height
		FROM messages
		WHERE chat_id = ?
	`
	args := []any{chatID}

	if beforeMessageID != "" {
		beforeMessage, err := db.GetMessage(ctx, beforeMessageID)
		if err != nil {
			return nil, err
		}

		query += `
			AND (timestamp < ? OR (timestamp = ? AND id < ?))
		`
		args = append(args, beforeMessage.TimestampUnix, beforeMessage.TimestampUnix, beforeMessage.ID)
	}

	query += `
		ORDER BY timestamp DESC, id DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := db.conn.QueryContext(ctx, query, args...)
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
			&message.MediaMimeType,
			&message.MediaLocalPath,
			&message.MediaWidth,
			&message.MediaHeight,
		); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	reverseMessages(messages)
	return messages, nil
}

func (db *DB) GetMessage(ctx context.Context, id string) (Message, error) {
	var message Message
	err := getMessageRow(ctx, db.conn, id, &message)
	return message, err
}

func (db *DB) ListPendingOutgoingMessages(ctx context.Context, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_mime_type, media_local_path, media_width, media_height
		FROM messages
		WHERE direction = ? AND status = ?
		ORDER BY timestamp ASC, id ASC
		LIMIT ?
	`, DirectionOutgoing, StatusPending, limit)
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
			&message.MediaMimeType,
			&message.MediaLocalPath,
			&message.MediaWidth,
			&message.MediaHeight,
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

func (db *DB) UpdateMessageStatus(ctx context.Context, id, status string) (Message, bool, error) {
	message, err := db.GetMessage(ctx, id)
	if err != nil {
		return Message{}, false, err
	}

	nextStatus, changed := nextMessageStatus(message.Status, status)
	if !changed {
		return message, false, nil
	}

	if _, err := db.conn.ExecContext(ctx, `
		UPDATE messages
		SET status = ?
		WHERE id = ?
	`, nextStatus, id); err != nil {
		return Message{}, false, err
	}

	message.Status = nextStatus
	return message, true, nil
}

func (db *DB) ReadCandidatesForChat(ctx context.Context, chatID string) ([]ReadCandidate, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, chat_id, sender_id, timestamp
		FROM messages
		WHERE chat_id = ? AND direction = ? AND is_read = 0
		ORDER BY timestamp ASC, id ASC
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
	err := queryer.QueryRowContext(ctx, `
		SELECT id, name, last_message, last_message_time, unread_count, is_group, avatar_local_path, avatar_picture_id
		FROM chats
		WHERE id = ?
	`, id).Scan(
		&chat.ID,
		&chat.Name,
		&chat.LastMessage,
		&chat.LastMessageTime,
		&chat.UnreadCount,
		&isGroup,
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
	)
	chat.IsGroup = isGroup != 0
	return chat, err
}

func getMessageRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, message *Message) error {
	return queryer.QueryRowContext(ctx, `
		SELECT id, chat_id, sender_id, text, timestamp, direction, is_read, status, media_mime_type, media_local_path, media_width, media_height
		FROM messages
		WHERE id = ?
	`, id).Scan(
		&message.ID,
		&message.ChatID,
		&message.SenderID,
		&message.Text,
		&message.TimestampUnix,
		&message.Direction,
		&message.IsRead,
		&message.Status,
		&message.MediaMimeType,
		&message.MediaLocalPath,
		&message.MediaWidth,
		&message.MediaHeight,
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
	err := scanner.Scan(
		&chat.ID,
		&chat.Name,
		&chat.LastMessage,
		&chat.LastMessageTime,
		&chat.UnreadCount,
		&isGroup,
		&chat.AvatarLocalPath,
		&chat.AvatarPictureID,
	)
	chat.IsGroup = isGroup != 0
	return chat, err
}

func reverseMessages(messages []Message) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
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
