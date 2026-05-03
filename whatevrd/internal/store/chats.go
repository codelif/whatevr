package store

import (
	"context"
	"database/sql"
	"strings"
)

type Chat struct {
	ID              string
	Name            string
	LastMessage     string
	LastMessageTime int64
	UnreadCount     int32
	IsGroup         bool
	AvatarLocalPath string
	AvatarPictureID string
}

func (db *DB) ListChats(ctx context.Context, limit, offset int) ([]Chat, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, name, last_message, last_message_time, unread_count, is_group, avatar_local_path, avatar_picture_id
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

func (db *DB) ClearSessionData(ctx context.Context) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, statement := range []string{
		`DELETE FROM messages`,
		`DELETE FROM chats`,
		`DELETE FROM app_state`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *DB) UpdateChatName(ctx context.Context, chatID, name string) (Chat, bool, error) {
	name = strings.TrimSpace(name)
	if chatID == "" || name == "" {
		return Chat{}, false, nil
	}

	result, err := db.conn.ExecContext(ctx, `
		UPDATE chats
		SET name = ?
		WHERE id = ? AND name != ?
	`, name, chatID, name)
	if err != nil {
		return Chat{}, false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Chat{}, false, err
	}
	if rowsAffected == 0 {
		return Chat{}, false, nil
	}

	chat, err := db.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, false, err
	}
	return chat, true, nil
}

func (db *DB) ListLIDChats(ctx context.Context) ([]string, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id FROM chats WHERE id LIKE '%@lid'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chatIDs := make([]string, 0)
	for rows.Next() {
		var chatID string
		if err := rows.Scan(&chatID); err != nil {
			return nil, err
		}
		chatIDs = append(chatIDs, chatID)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return chatIDs, nil
}

func (db *DB) UpdateChatAvatar(ctx context.Context, chatID, picID, localPath string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE chats SET avatar_picture_id = ?, avatar_local_path = ? WHERE id = ?
	`, picID, localPath, chatID)
	return err
}

func (db *DB) GetChatAvatarPictureID(ctx context.Context, chatID string) (string, error) {
	var picID string
	err := db.conn.QueryRowContext(ctx, `SELECT avatar_picture_id FROM chats WHERE id = ?`, chatID).Scan(&picID)
	return picID, err
}

func (db *DB) MigrateChatID(ctx context.Context, fromChatID, toChatID string) (Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, false, err
	}
	defer tx.Rollback()

	var sourceExists int
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM chats
		WHERE id = ?
	`, fromChatID).Scan(&sourceExists)
	if err != nil {
		if err == sql.ErrNoRows {
			return Chat{}, false, nil
		}
		return Chat{}, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chats (id, name, last_message, last_message_time, unread_count, is_group, avatar_local_path, avatar_picture_id)
		SELECT ?, name, last_message, last_message_time, unread_count, is_group, avatar_local_path, avatar_picture_id
		FROM chats
		WHERE id = ?
		ON CONFLICT(id) DO UPDATE SET
			name = CASE
				WHEN chats.name = ? OR chats.name = ''
				THEN excluded.name
				ELSE chats.name
			END,
			last_message = CASE
				WHEN excluded.last_message_time >= chats.last_message_time
				THEN excluded.last_message
				ELSE chats.last_message
			END,
			last_message_time = MAX(chats.last_message_time, excluded.last_message_time),
			unread_count = chats.unread_count + excluded.unread_count,
			is_group = excluded.is_group
	`, toChatID, fromChatID, toChatID); err != nil {
		return Chat{}, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO messages
		(id, chat_id, sender_id, text, timestamp, direction, is_read, status)
		SELECT
			replace(id, ? || ':', ? || ':'),
			?, sender_id, text, timestamp, direction, is_read, status
		FROM messages
		WHERE chat_id = ?
	`, fromChatID, toChatID, toChatID, fromChatID); err != nil {
		return Chat{}, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM chats WHERE id = ?
	`, fromChatID); err != nil {
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
	`, toChatID, DirectionIncoming, toChatID); err != nil {
		return Chat{}, false, err
	}

	chat, err := getChatTx(ctx, tx, toChatID)
	if err != nil {
		return Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Chat{}, false, err
	}

	return chat, true, nil
}
