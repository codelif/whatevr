package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	DirectionIncoming = "incoming"
	DirectionOutgoing = "outgoing"

	StatusReceived = "delivered"
	StatusSent     = "sent"
)

type Message struct {
	ID            string
	ChatID        string
	SenderID      string
	Text          string
	TimestampUnix int64
	Direction     string
	Status        string
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
		input.Status = StatusReceived
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
		INSERT INTO messages (id, chat_id, sender_id, text, timestamp, direction, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.ChatID, input.SenderID, input.Text, input.Timestamp.Unix(), input.Direction, input.Status)
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
	err := tx.QueryRowContext(ctx, `
		SELECT id, chat_id, sender_id, text, timestamp, direction, status
		FROM messages
		WHERE id = ?
	`, id).Scan(
		&message.ID,
		&message.ChatID,
		&message.SenderID,
		&message.Text,
		&message.TimestampUnix,
		&message.Direction,
		&message.Status,
	)
	return message, err
}

func getChatTx(ctx context.Context, tx *sql.Tx, id string) (Chat, error) {
	var chat Chat
	var isGroup int
	err := tx.QueryRowContext(ctx, `
		SELECT id, name, last_message, last_message_time, unread_count, is_group
		FROM chats
		WHERE id = ?
	`, id).Scan(
		&chat.ID,
		&chat.Name,
		&chat.LastMessage,
		&chat.LastMessageTime,
		&chat.UnreadCount,
		&isGroup,
	)
	chat.IsGroup = isGroup != 0
	return chat, err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
