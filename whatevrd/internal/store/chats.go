package store

import (
	"context"
	"database/sql"
	"strings"
)

type Chat struct {
	ID                   string
	Name                 string
	NameSource           string
	LastMessage          string
	LastMessageTime      int64
	LastMessageDirection string
	LastMessageStatus    string
	UnreadCount          int32
	IsGroup              bool
	IsPinned             bool
	PinnedOrder          uint32
	AvatarLocalPath      string
	AvatarPictureID      string
	AvatarStatus         string
	AvatarCheckedAt      int64
}

const (
	ChatNameSourceRaw      = "raw"
	ChatNameSourceWhatsApp = "whatsapp"
	ChatNameSourcePhone    = "phone"
	ChatNameSourceGroup    = "group"
	ChatNameSourceContact  = "contact"
)

func normalizeChatNameSource(source string) string {
	switch strings.TrimSpace(source) {
	case ChatNameSourceRaw, ChatNameSourceWhatsApp, ChatNameSourcePhone, ChatNameSourceGroup, ChatNameSourceContact:
		return strings.TrimSpace(source)
	default:
		return ChatNameSourceContact
	}
}

func chatNameSourcePriority(source string) int {
	switch normalizeChatNameSource(source) {
	case ChatNameSourceRaw:
		return 0
	case ChatNameSourceWhatsApp:
		return 1
	case ChatNameSourcePhone:
		return 2
	case ChatNameSourceGroup:
		return 3
	case ChatNameSourceContact:
		return 4
	default:
		return 4
	}
}

func (db *DB) ListChats(ctx context.Context, limit, offset int) ([]Chat, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := db.conn.QueryContext(ctx, `
		SELECT c.id, c.name, c.name_source, c.last_message, c.last_message_time, c.last_message_direction, c.last_message_status, c.unread_count, c.is_group, c.is_pinned, c.pinned_order,
		       COALESCE(NULLIF(a.local_path, ''), c.avatar_local_path), COALESCE(NULLIF(a.picture_id, ''), c.avatar_picture_id), COALESCE(NULLIF(a.status, ''), c.avatar_status), COALESCE(NULLIF(a.checked_at, 0), c.avatar_checked_at)
		FROM chats c
		LEFT JOIN avatars a ON a.subject_kind = 'chat' AND a.subject_id = c.id
		ORDER BY CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END, c.pinned_order DESC, c.last_message_time DESC, c.id ASC
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

func (db *DB) EnsureChat(ctx context.Context, chatID, name string, isGroup bool) (Chat, error) {
	return db.EnsureChatWithNameSource(ctx, chatID, name, ChatNameSourceContact, isGroup)
}

func (db *DB) EnsureChatWithNameSource(ctx context.Context, chatID, name, nameSource string, isGroup bool) (Chat, error) {
	if chatID == "" {
		return Chat{}, nil
	}
	name = strings.TrimSpace(name)
	nameSource = normalizeChatNameSource(nameSource)
	insertName := name
	if insertName == "" {
		insertName = chatID
		nameSource = ChatNameSourceRaw
	}

	if _, err := db.conn.ExecContext(ctx, `
		INSERT INTO chats (id, name, name_source, last_message, last_message_time, last_message_direction, last_message_status, unread_count, is_group, is_pinned, pinned_order)
		VALUES (?, ?, ?, '', 0, '', '', 0, ?, 0, 0)
		ON CONFLICT(id) DO UPDATE SET
			name = CASE WHEN ? != '' AND chat_name_source_priority(?) >= chat_name_source_priority(chats.name_source) THEN ? ELSE chats.name END,
			name_source = CASE WHEN ? != '' AND chat_name_source_priority(?) >= chat_name_source_priority(chats.name_source) THEN ? ELSE chats.name_source END,
			is_group = ?
	`, chatID, insertName, nameSource, boolToInt(isGroup), name, nameSource, name, name, nameSource, nameSource, boolToInt(isGroup)); err != nil {
		return Chat{}, err
	}

	return db.GetChat(ctx, chatID)
}

func (db *DB) UpdateChatPinState(ctx context.Context, chatID string, pinned bool, order uint32) (Chat, bool, error) {
	if chatID == "" {
		return Chat{}, false, nil
	}
	if !pinned {
		order = 0
	}

	result, err := db.conn.ExecContext(ctx, `
		UPDATE chats
		SET is_pinned = ?, pinned_order = ?
		WHERE id = ? AND (is_pinned != ? OR pinned_order != ?)
	`, boolToInt(pinned), order, chatID, boolToInt(pinned), order)
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

func (db *DB) PinnedChatCountExcluding(ctx context.Context, chatID string) (int, error) {
	var count int
	err := db.conn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM chats
		WHERE is_pinned != 0 AND id != ?
	`, chatID).Scan(&count)
	return count, err
}

func (db *DB) ReconcileChatPins(ctx context.Context, pins map[string]uint32) ([]Chat, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, is_pinned, pinned_order
		FROM chats
	`)
	if err != nil {
		return nil, err
	}

	type pinState struct {
		pinned bool
		order  uint32
	}
	current := make(map[string]pinState)
	for rows.Next() {
		var id string
		var isPinned int
		var order uint32
		if err := rows.Scan(&id, &isPinned, &order); err != nil {
			rows.Close()
			return nil, err
		}
		current[id] = pinState{pinned: isPinned != 0, order: order}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	changedIDs := make([]string, 0)
	for id, state := range current {
		order, shouldPin := pins[id]
		if !shouldPin {
			order = 0
		}
		if state.pinned == shouldPin && state.order == order {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE chats
			SET is_pinned = ?, pinned_order = ?
			WHERE id = ?
		`, boolToInt(shouldPin), order, id); err != nil {
			return nil, err
		}
		changedIDs = append(changedIDs, id)
	}

	changed := make([]Chat, 0, len(changedIDs))
	for _, id := range changedIDs {
		chat, err := getChatTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		changed = append(changed, chat)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}

func (db *DB) ClearChatPins(ctx context.Context) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE chats
		SET is_pinned = 0, pinned_order = 0
		WHERE is_pinned != 0 OR pinned_order != 0
	`)
	return err
}

func (db *DB) ClearSessionData(ctx context.Context) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, statement := range []string{
		`DELETE FROM history_sync_chunks`,
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

// OverwriteChatUnreadCount sets the chat's unread_count to the given value
// directly. When unread is 0, all incoming messages in the chat are also
// marked is_read=1 so the badge agrees with per-message state.
//
// This is intended for history sync, where the phone sends authoritative
// per-conversation read state and we want the chat list badge to mirror it
// (instead of the sum of locally inserted unread rows, which is always 0
// because history sync inserts run with CountUnread=false).
//
// Returns the chat row (post-update) and whether anything actually changed.
func (db *DB) OverwriteChatUnreadCount(ctx context.Context, chatID string, unread uint32) (Chat, bool, error) {
	if chatID == "" {
		return Chat{}, false, nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, false, err
	}
	defer tx.Rollback()

	current, err := getChatTx(ctx, tx, chatID)
	if err != nil {
		return Chat{}, false, err
	}

	changed := int32(unread) != current.UnreadCount
	if changed {
		if _, err := tx.ExecContext(ctx, `
			UPDATE chats
			SET unread_count = ?
			WHERE id = ?
		`, int32(unread), chatID); err != nil {
			return Chat{}, false, err
		}
	}

	if unread == 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET is_read = 1
			WHERE chat_id = ? AND direction = ? AND is_read = 0
		`, chatID, DirectionIncoming); err != nil {
			return Chat{}, false, err
		}
	}

	chat, err := getChatTx(ctx, tx, chatID)
	if err != nil {
		return Chat{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Chat{}, false, err
	}

	return chat, changed, nil
}

func (db *DB) UpdateChatName(ctx context.Context, chatID, name string) (Chat, bool, error) {
	return db.UpdateChatNameWithSource(ctx, chatID, name, ChatNameSourceContact)
}

func (db *DB) UpdateChatNameWithSource(ctx context.Context, chatID, name, nameSource string) (Chat, bool, error) {
	name = strings.TrimSpace(name)
	if chatID == "" || name == "" {
		return Chat{}, false, nil
	}
	nameSource = normalizeChatNameSource(nameSource)

	result, err := db.conn.ExecContext(ctx, `
		UPDATE chats
		SET name = ?, name_source = ?
		WHERE id = ?
			AND (name != ? OR name_source != ?)
			AND chat_name_source_priority(?) >= chat_name_source_priority(name_source)
	`, name, nameSource, chatID, name, nameSource, nameSource)
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

func (db *DB) ListRawGroupChatIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := db.conn.QueryContext(ctx, `
		SELECT id
		FROM chats
		WHERE is_group = 1
		  AND id LIKE '%@g.us'
		  AND (name = '' OR name = id OR name_source = ?)
		ORDER BY last_message_time DESC, id ASC
		LIMIT ?
	`, ChatNameSourceRaw, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chatIDs := make([]string, 0, limit)
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
		UPDATE chats SET avatar_picture_id = ?, avatar_local_path = ?, avatar_status = '', avatar_checked_at = unixepoch() WHERE id = ?
	`, picID, localPath, chatID)
	return err
}

func (db *DB) UpdateChatAvatarStatus(ctx context.Context, chatID, status string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE chats SET avatar_status = ?, avatar_checked_at = unixepoch() WHERE id = ?
	`, status, chatID)
	return err
}

func (db *DB) ClearChatAvatar(ctx context.Context, chatID, status string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE chats SET avatar_picture_id = '', avatar_local_path = '', avatar_status = ?, avatar_checked_at = unixepoch() WHERE id = ?
	`, status, chatID)
	return err
}

type SenderProfile struct {
	ID              string
	Name            string
	AvatarLocalPath string
	AvatarPictureID string
	AvatarStatus    string
	AvatarCheckedAt int64
}

func (db *DB) ListSendersNeedingAvatar(ctx context.Context, limit int) ([]SenderProfile, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, name, avatar_local_path, avatar_picture_id, avatar_status, avatar_checked_at
		FROM senders
		WHERE id != 'me' AND (avatar_picture_id = '' OR avatar_local_path = '')
		  AND (avatar_status = '' OR avatar_checked_at <= unixepoch() - 86400)
		ORDER BY id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	senders := make([]SenderProfile, 0, limit)
	for rows.Next() {
		var sender SenderProfile
		if err := rows.Scan(&sender.ID, &sender.Name, &sender.AvatarLocalPath, &sender.AvatarPictureID, &sender.AvatarStatus, &sender.AvatarCheckedAt); err != nil {
			return nil, err
		}
		senders = append(senders, sender)
	}
	return senders, rows.Err()
}

func (db *DB) ListSendersForAvatarRefresh(ctx context.Context, limit int) ([]SenderProfile, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, name, avatar_local_path, avatar_picture_id, avatar_status, avatar_checked_at
		FROM senders
		WHERE id != 'me'
		  AND (avatar_status = '' OR avatar_checked_at <= unixepoch() - 86400)
		ORDER BY avatar_checked_at ASC, id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	senders := make([]SenderProfile, 0, limit)
	for rows.Next() {
		var sender SenderProfile
		if err := rows.Scan(&sender.ID, &sender.Name, &sender.AvatarLocalPath, &sender.AvatarPictureID, &sender.AvatarStatus, &sender.AvatarCheckedAt); err != nil {
			return nil, err
		}
		senders = append(senders, sender)
	}
	return senders, rows.Err()
}

func (db *DB) GetSenderProfile(ctx context.Context, senderID string) (SenderProfile, error) {
	var sender SenderProfile
	err := db.conn.QueryRowContext(ctx, `
		SELECT id, name, avatar_local_path, avatar_picture_id, avatar_status, avatar_checked_at
		FROM senders
		WHERE id = ?
	`, senderID).Scan(&sender.ID, &sender.Name, &sender.AvatarLocalPath, &sender.AvatarPictureID, &sender.AvatarStatus, &sender.AvatarCheckedAt)
	return sender, err
}

func (db *DB) ListSenderProfilesByChatID(ctx context.Context, chatID string, limit int) ([]SenderProfile, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT s.id, s.name, s.avatar_local_path, s.avatar_picture_id, s.avatar_status, s.avatar_checked_at
		FROM senders s
		JOIN (
			SELECT sender_id, MAX(timestamp) AS last_message_time
			FROM messages
			WHERE chat_id = ? AND sender_id != 'me'
			GROUP BY sender_id
			ORDER BY last_message_time DESC
			LIMIT ?
		) recent ON recent.sender_id = s.id
		ORDER BY recent.last_message_time DESC, s.id ASC
	`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	senders := make([]SenderProfile, 0, limit)
	for rows.Next() {
		var sender SenderProfile
		if err := rows.Scan(&sender.ID, &sender.Name, &sender.AvatarLocalPath, &sender.AvatarPictureID, &sender.AvatarStatus, &sender.AvatarCheckedAt); err != nil {
			return nil, err
		}
		senders = append(senders, sender)
	}
	return senders, rows.Err()
}

func (db *DB) UpdateSenderAvatar(ctx context.Context, senderID, picID, localPath string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE senders SET avatar_picture_id = ?, avatar_local_path = ?, avatar_status = '', avatar_checked_at = unixepoch() WHERE id = ?
	`, picID, localPath, senderID)
	return err
}

func (db *DB) UpdateSenderAvatarStatus(ctx context.Context, senderID, status string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE senders SET avatar_status = ?, avatar_checked_at = unixepoch() WHERE id = ?
	`, status, senderID)
	return err
}

func (db *DB) ClearSenderAvatar(ctx context.Context, senderID, status string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE senders SET avatar_picture_id = '', avatar_local_path = '', avatar_status = ?, avatar_checked_at = unixepoch() WHERE id = ?
	`, status, senderID)
	return err
}

func (db *DB) UpdateSenderName(ctx context.Context, senderID, name string) error {
	name = strings.TrimSpace(name)
	if senderID == "" || name == "" || senderID == "me" {
		return nil
	}
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO senders (id, name)
		VALUES (?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name
	`, senderID, name)
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
		INSERT INTO chats (id, name, name_source, last_message, last_message_time, last_message_direction, last_message_status, unread_count, is_group, is_pinned, pinned_order, avatar_local_path, avatar_picture_id)
		SELECT ?, name, name_source, last_message, last_message_time, last_message_direction, last_message_status, unread_count, is_group, is_pinned, pinned_order, avatar_local_path, avatar_picture_id
		FROM chats
		WHERE id = ?
		ON CONFLICT(id) DO UPDATE SET
			name = CASE
				WHEN chats.name_source = ? OR chats.name = ? OR chats.name = ''
				THEN excluded.name
				ELSE chats.name
			END,
			name_source = CASE
				WHEN chats.name_source = ? OR chats.name = ? OR chats.name = ''
				THEN excluded.name_source
				ELSE chats.name_source
			END,
			last_message = CASE
				WHEN excluded.last_message_time >= chats.last_message_time
				THEN excluded.last_message
				ELSE chats.last_message
			END,
			last_message_direction = CASE
				WHEN excluded.last_message_time >= chats.last_message_time
				THEN excluded.last_message_direction
				ELSE chats.last_message_direction
			END,
			last_message_status = CASE
				WHEN excluded.last_message_time >= chats.last_message_time
				THEN excluded.last_message_status
				ELSE chats.last_message_status
			END,
			last_message_time = MAX(chats.last_message_time, excluded.last_message_time),
			unread_count = chats.unread_count + excluded.unread_count,
			is_pinned = excluded.is_pinned,
			pinned_order = excluded.pinned_order,
			is_group = excluded.is_group
	`, toChatID, fromChatID, ChatNameSourceRaw, toChatID, ChatNameSourceRaw, toChatID); err != nil {
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
