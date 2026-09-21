package store

import (
	"context"
	"time"
)

type ScheduledMessage struct {
	ID     int64
	ChatID string
	Text   string
	SendAt int64
}

func (db *DB) ScheduleText(ctx context.Context, chatID, text string, sendAt time.Time) (int64, error) {
	result, err := db.conn.ExecContext(ctx, `
		INSERT INTO scheduled_messages (chat_id, text, send_at) VALUES (?, ?, ?)
	`, chatID, text, sendAt.Unix())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (db *DB) DueScheduledMessages(ctx context.Context, now time.Time, limit int) ([]ScheduledMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.reader().QueryContext(ctx, `
		SELECT id, chat_id, text, send_at
		FROM scheduled_messages WHERE send_at <= ? ORDER BY send_at, id LIMIT ?
	`, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []ScheduledMessage
	for rows.Next() {
		var message ScheduledMessage
		if err := rows.Scan(&message.ID, &message.ChatID, &message.Text, &message.SendAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (db *DB) DeleteScheduledMessage(ctx context.Context, id int64) error {
	_, err := db.conn.ExecContext(ctx, `DELETE FROM scheduled_messages WHERE id = ?`, id)
	return err
}

// ListScheduledMessages returns pending scheduled sends, soonest first.
// An empty chatID lists every chat; otherwise only that chat's rows.
func (db *DB) ListScheduledMessages(ctx context.Context, chatID string, limit int) ([]ScheduledMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	var query string
	var args []any
	if chatID == "" {
		query = `SELECT id, chat_id, text, send_at FROM scheduled_messages ORDER BY send_at, id LIMIT ?`
		args = []any{limit}
	} else {
		query = `SELECT id, chat_id, text, send_at FROM scheduled_messages WHERE chat_id = ? ORDER BY send_at, id LIMIT ?`
		args = []any{chatID, limit}
	}
	r, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	messages := []ScheduledMessage{}
	for r.Next() {
		var message ScheduledMessage
		if err := r.Scan(&message.ID, &message.ChatID, &message.Text, &message.SendAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, r.Err()
}
