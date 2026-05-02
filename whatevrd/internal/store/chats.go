package store

import "context"

type Chat struct {
	ID              string
	Name            string
	LastMessage     string
	LastMessageTime int64
	UnreadCount     int32
	IsGroup         bool
}

func (db *DB) ListChats(ctx context.Context, limit, offset int) ([]Chat, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, name, last_message, last_message_time, unread_count, is_group
		FROM chats
		ORDER BY last_message_time DESC, id ASC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := make([]Chat, 0, limit)
	for rows.Next() {
		chat, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return chats, nil
}

func (db *DB) MarkChatRead(ctx context.Context, chatID string) (Chat, error) {
	return db.MarkMessagesRead(ctx, chatID)
}

func (db *DB) GetChat(ctx context.Context, chatID string) (Chat, error) {
	return getChatRow(ctx, db.conn, chatID)
}
