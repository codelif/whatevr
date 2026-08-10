package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"
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
	IsArchived           bool
	IsMuted              bool
	MuteEndTimestamp     int64
	HistoryExhausted     bool
	UpdatedAt            int64
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

// ListChats returns a page of chats in list order (pinned first, then most
// recent). When afterChatID names an existing chat, the page starts strictly
// after it (keyset pagination, stable under reordering); otherwise the legacy
// offset is applied.
func (db *DB) ListChats(ctx context.Context, limit, offset int, afterChatID string) ([]Chat, error) {
	defer db.timeOp("ListChats", time.Now())
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	const selectColumns = `
		SELECT c.id, c.name, c.name_source, c.last_message, c.last_message_time, c.last_message_direction, c.last_message_status, c.unread_count, c.is_group, c.is_pinned, c.pinned_order, c.updated_at, c.is_archived, c.is_muted, c.mute_end_timestamp, c.history_exhausted,
		       COALESCE(NULLIF(a.local_path, ''), c.avatar_local_path), COALESCE(NULLIF(a.picture_id, ''), c.avatar_picture_id), COALESCE(NULLIF(a.status, ''), c.avatar_status), COALESCE(NULLIF(a.checked_at, 0), c.avatar_checked_at)
		FROM chats c
		LEFT JOIN avatars a ON a.subject_kind = 'chat' AND a.subject_id = c.id
	`
	const orderBy = `
		ORDER BY CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END, c.pinned_order DESC, c.last_message_time DESC, c.id ASC
	`

	query := selectColumns
	args := []any{}

	if afterChatID != "" {
		var bucket, pinnedOrder int64
		var lastMessageTime int64
		err := db.reader().QueryRowContext(ctx, `
			SELECT CASE WHEN is_pinned != 0 THEN 0 ELSE 1 END, pinned_order, last_message_time
			FROM chats WHERE id = ?
		`, afterChatID).Scan(&bucket, &pinnedOrder, &lastMessageTime)
		switch {
		case err == sql.ErrNoRows:
			// Cursor chat disappeared; fall back to the start of the list.
			afterChatID = ""
		case err != nil:
			return nil, err
		default:
			// Keyset matching the ORDER BY above (bucket ASC, pinned_order
			// DESC, last_message_time DESC, id ASC).
			query += `
				WHERE (CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END) > ?
				   OR ((CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END) = ? AND c.pinned_order < ?)
				   OR ((CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END) = ? AND c.pinned_order = ? AND c.last_message_time < ?)
				   OR ((CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END) = ? AND c.pinned_order = ? AND c.last_message_time = ? AND c.id > ?)
			`
			args = append(args,
				bucket,
				bucket, pinnedOrder,
				bucket, pinnedOrder, lastMessageTime,
				bucket, pinnedOrder, lastMessageTime, afterChatID,
			)
		}
	}

	query += orderBy + ` LIMIT ?`
	args = append(args, limit)
	if afterChatID == "" && offset > 0 {
		query += ` OFFSET ?`
		args = append(args, offset)
	}

	rows, err := db.reader().QueryContext(ctx, query, args...)
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
		chat = normalizeListedChatName(chat)
		chats = append(chats, chat)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return chats, nil
}

// Chat list filters for ListChatsForView. The empty string means both direct
// and group chats.
const (
	ChatFilterAll    = ""
	ChatFilterDirect = "direct"
	ChatFilterGroups = "groups"
)

// ChatListFilter selects which chats ListChatsForView returns.
type ChatListFilter struct {
	Kind     string // ChatFilterAll | ChatFilterDirect | ChatFilterGroups
	Archived bool   // archived tab (true) vs the main list (false)
	Limit    int    // <= 0 means no limit (whole filtered list)
}

// ListChatsForView returns chats matching filter in list order (pinned first,
// then most recent). Unlike ListChats it segregates archived from unarchived
// and can restrict to direct or group chats, which is what the protocol
// `chats` view subscribes to. The protocol view engine re-reads the whole
// window on every change, so this is a plain LIMIT rather than keyset paging.
func (db *DB) ListChatsForView(ctx context.Context, filter ChatListFilter) ([]Chat, error) {
	defer db.timeOp("ListChatsForView", time.Now())

	query := `
		SELECT c.id, c.name, c.name_source, c.last_message, c.last_message_time, c.last_message_direction, c.last_message_status, c.unread_count, c.is_group, c.is_pinned, c.pinned_order, c.updated_at, c.is_archived, c.is_muted, c.mute_end_timestamp, c.history_exhausted,
		       COALESCE(NULLIF(a.local_path, ''), c.avatar_local_path), COALESCE(NULLIF(a.picture_id, ''), c.avatar_picture_id), COALESCE(NULLIF(a.status, ''), c.avatar_status), COALESCE(NULLIF(a.checked_at, 0), c.avatar_checked_at)
		FROM chats c
		LEFT JOIN avatars a ON a.subject_kind = 'chat' AND a.subject_id = c.id
		WHERE c.is_archived = ?
	`
	args := []any{boolToInt(filter.Archived)}

	switch filter.Kind {
	case ChatFilterDirect:
		query += ` AND c.is_group = 0`
	case ChatFilterGroups:
		query += ` AND c.is_group = 1`
	}

	query += `
		ORDER BY CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END, c.pinned_order DESC, c.last_message_time DESC, c.id ASC
	`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := make([]Chat, 0)
	for rows.Next() {
		chat, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, normalizeListedChatName(chat))
	}
	return chats, rows.Err()
}

// SearchChats returns chats whose display name matches query (case-insensitive
// substring), in the same list order as ListChats. The chat table is small, so
// a LIKE scan is fine; message text search uses the FTS index instead. A blank
// query returns no rows.
func (db *DB) SearchChats(ctx context.Context, query string, limit int) ([]Chat, error) {
	defer db.timeOp("SearchChats", time.Now())
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.reader().QueryContext(ctx, `
		SELECT c.id, c.name, c.name_source, c.last_message, c.last_message_time, c.last_message_direction, c.last_message_status, c.unread_count, c.is_group, c.is_pinned, c.pinned_order, c.updated_at, c.is_archived, c.is_muted, c.mute_end_timestamp, c.history_exhausted,
		       COALESCE(NULLIF(a.local_path, ''), c.avatar_local_path), COALESCE(NULLIF(a.picture_id, ''), c.avatar_picture_id), COALESCE(NULLIF(a.status, ''), c.avatar_status), COALESCE(NULLIF(a.checked_at, 0), c.avatar_checked_at)
		FROM chats c
		LEFT JOIN avatars a ON a.subject_kind = 'chat' AND a.subject_id = c.id
		WHERE c.name LIKE ? ESCAPE '\' COLLATE NOCASE
		ORDER BY CASE WHEN c.is_pinned != 0 THEN 0 ELSE 1 END, c.pinned_order DESC, c.last_message_time DESC, c.id ASC
		LIMIT ?
	`, "%"+escapeLike(query)+"%", limit)
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
		chats = append(chats, normalizeListedChatName(chat))
	}
	return chats, rows.Err()
}

func normalizeListedChatName(chat Chat) Chat {
	if chat.IsGroup || chat.NameSource != ChatNameSourceWhatsApp {
		return chat
	}
	if phone := formatDirectChatPhoneDisplayName(chat.ID); phone != "" {
		chat.Name = phone
		chat.NameSource = ChatNameSourcePhone
	}
	return chat
}

func formatDirectChatPhoneDisplayName(chatID string) string {
	const defaultUserSuffix = "@s.whatsapp.net"
	user, ok := strings.CutSuffix(chatID, defaultUserSuffix)
	if !ok || user == "" {
		return ""
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, user)
	if digits == "" {
		return ""
	}
	number, err := phonenumbers.Parse("+"+digits, "ZZ")
	if err != nil || !phonenumbers.IsValidNumber(number) {
		return "+" + digits
	}
	return phonenumbers.Format(number, phonenumbers.INTERNATIONAL)
}

func (db *DB) MarkChatRead(ctx context.Context, chatID string) (Chat, error) {
	return db.MarkMessagesRead(ctx, chatID)
}

func (db *DB) GetChat(ctx context.Context, chatID string) (Chat, error) {
	return getChatRow(ctx, db.reader(), chatID)
}

// GetChatForView returns one chat with the same display-name normalization as
// ListChatsForView and SearchChats. GetChat intentionally preserves the stored
// WhatsApp push-name fallback for daemon-internal callers.
func (db *DB) GetChatForView(ctx context.Context, chatID string) (Chat, error) {
	chat, err := db.GetChat(ctx, chatID)
	if err != nil {
		return Chat{}, err
	}
	return normalizeListedChatName(chat), nil
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

// DeleteChat removes a chat and everything hanging off it (messages via
// explicit delete so the FTS triggers fire, reactions/receipts via FK
// cascade, plus the trigger-less side tables). Returns whether a chat row
// existed.
func (db *DB) DeleteChat(ctx context.Context, chatID string) (bool, error) {
	if chatID == "" {
		return false, nil
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE chat_id = ?`, chatID); err != nil {
		return false, err
	}
	for _, statement := range []string{
		`DELETE FROM undecryptable_messages WHERE chat_id = ?`,
		`DELETE FROM group_participants WHERE chat_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, chatID); err != nil {
			return false, err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM chats WHERE id = ?`, chatID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected > 0, nil
}

// ClearChatMessages wipes a chat's transcript but keeps the chat row.
// Returns the refreshed chat and whether it existed.
func (db *DB) ClearChatMessages(ctx context.Context, chatID string) (Chat, bool, error) {
	if chatID == "" {
		return Chat{}, false, nil
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE chat_id = ?`, chatID); err != nil {
		return Chat{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM undecryptable_messages WHERE chat_id = ?`, chatID); err != nil {
		return Chat{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET unread_count = 0 WHERE id = ?`, chatID); err != nil {
		return Chat{}, false, err
	}
	if err := recomputeChatSummaryTx(ctx, tx, chatID); err != nil {
		return Chat{}, false, err
	}
	chat, err := getChatTx(ctx, tx, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, false, nil
	}
	if err != nil {
		return Chat{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, false, err
	}
	return chat, true, nil
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

func (db *DB) UpdateChatArchiveState(ctx context.Context, chatID string, archived bool) (Chat, bool, error) {
	if chatID == "" {
		return Chat{}, false, nil
	}

	result, err := db.conn.ExecContext(ctx, `
		UPDATE chats
		SET is_archived = ?
		WHERE id = ? AND is_archived != ?
	`, boolToInt(archived), chatID, boolToInt(archived))
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

// UpdateChatHistoryExhausted records whether the phone has answered an
// on-demand history request for this chat with nothing older than what is
// stored locally.
func (db *DB) UpdateChatHistoryExhausted(ctx context.Context, chatID string, exhausted bool) (Chat, bool, error) {
	if chatID == "" {
		return Chat{}, false, nil
	}

	result, err := db.conn.ExecContext(ctx, `
		UPDATE chats
		SET history_exhausted = ?
		WHERE id = ? AND history_exhausted != ?
	`, boolToInt(exhausted), chatID, boolToInt(exhausted))
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

// UpdateChatMuteState persists the chat's mute flag and expiry (unix millis;
// -1 = forever, 0 = not muted). When unmuting, muteEnd is forced to 0.
func (db *DB) UpdateChatMuteState(ctx context.Context, chatID string, muted bool, muteEnd int64) (Chat, bool, error) {
	if chatID == "" {
		return Chat{}, false, nil
	}
	if !muted {
		muteEnd = 0
	}

	result, err := db.conn.ExecContext(ctx, `
		UPDATE chats
		SET is_muted = ?, mute_end_timestamp = ?
		WHERE id = ? AND (is_muted != ? OR mute_end_timestamp != ?)
	`, boolToInt(muted), muteEnd, chatID, boolToInt(muted), muteEnd)
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

// ReconcileChatArchives sets is_archived to match the given set of archived chat
// IDs exactly (chats not in the set are unarchived), returning the rows that
// changed. Mirrors ReconcileChatPins; used when a full app-state sync lands.
func (db *DB) ReconcileChatArchives(ctx context.Context, archived map[string]struct{}) ([]Chat, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id, is_archived FROM chats`)
	if err != nil {
		return nil, err
	}

	current := make(map[string]bool)
	for rows.Next() {
		var id string
		var isArchived int
		if err := rows.Scan(&id, &isArchived); err != nil {
			rows.Close()
			return nil, err
		}
		current[id] = isArchived != 0
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	changedIDs := make([]string, 0)
	for id, isArchived := range current {
		_, shouldArchive := archived[id]
		if isArchived == shouldArchive {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE chats SET is_archived = ? WHERE id = ?
		`, boolToInt(shouldArchive), id); err != nil {
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

func (db *DB) PinnedChatCountExcluding(ctx context.Context, chatID string) (int, error) {
	var count int
	err := db.reader().QueryRowContext(ctx, `
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

// ReconcileChatMutes sets mute state to match the given map exactly (chats
// not in the map are unmuted), returning the rows that changed. Mirrors
// ReconcileChatPins; used when a full app-state sync lands. Map values are
// the mute end in unix millis, -1 meaning "muted forever".
func (db *DB) ReconcileChatMutes(ctx context.Context, mutes map[string]int64) ([]Chat, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id, is_muted, mute_end_timestamp FROM chats`)
	if err != nil {
		return nil, err
	}

	type muteState struct {
		muted bool
		end   int64
	}
	current := make(map[string]muteState)
	for rows.Next() {
		var id string
		var isMuted int
		var end int64
		if err := rows.Scan(&id, &isMuted, &end); err != nil {
			rows.Close()
			return nil, err
		}
		current[id] = muteState{muted: isMuted != 0, end: end}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	changedIDs := make([]string, 0)
	for id, state := range current {
		end, shouldMute := mutes[id]
		if !shouldMute {
			end = 0
		}
		if state.muted == shouldMute && state.end == end {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE chats SET is_muted = ?, mute_end_timestamp = ? WHERE id = ?
		`, boolToInt(shouldMute), end, id); err != nil {
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

// clearSessionKeepTables are the tables that survive a logout because they
// hold machine-level preferences rather than account data. Every other table
// in sqlite_master is wiped, so a newly added table is cleared by default
// instead of leaking into the next account's session.
var clearSessionKeepTables = map[string]bool{
	"daemon_config": true,
}

func (db *DB) ClearSessionData(ctx context.Context) error {
	regular, virtual, err := db.listTables(ctx)
	if err != nil {
		return err
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Every row of every table goes at once, so enforce foreign keys at
	// commit time; delete order across tables then doesn't matter.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	for _, table := range regular {
		if clearSessionKeepTables[table] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM "`+table+`"`); err != nil {
			return fmt.Errorf("clear table %s: %w", table, err)
		}
	}
	// The only virtual tables in the schema are external-content FTS5 indexes
	// (messages_fts). Their content tables were just emptied, so 'rebuild'
	// resets the index to a consistent empty state.
	for _, table := range virtual {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO "%s"("%s") VALUES('rebuild')`, table, table)); err != nil {
			return fmt.Errorf("rebuild virtual table %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	_, err = db.conn.ExecContext(ctx, `VACUUM`)
	return err
}

// listTables enumerates user tables from sqlite_master, separating virtual
// tables from regular ones. Shadow tables backing a virtual table (e.g.
// messages_fts_data) are excluded entirely: they are managed by the virtual
// table module and must never be written directly.
func (db *DB) listTables(ctx context.Context) (regular, virtual []string, err error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var all []string
	for rows.Next() {
		var name string
		var createSQL sql.NullString
		if err := rows.Scan(&name, &createSQL); err != nil {
			return nil, nil, err
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(createSQL.String)), "CREATE VIRTUAL TABLE") {
			virtual = append(virtual, name)
			continue
		}
		all = append(all, name)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	for _, name := range all {
		shadow := false
		for _, v := range virtual {
			if strings.HasPrefix(name, v+"_") {
				shadow = true
				break
			}
		}
		if !shadow {
			regular = append(regular, name)
		}
	}
	return regular, virtual, nil
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

	rows, err := db.reader().QueryContext(ctx, `
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
	rows, err := db.reader().QueryContext(ctx, `
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
	rows, err := db.reader().QueryContext(ctx, `
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
	rows, err := db.reader().QueryContext(ctx, `
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
	err := db.reader().QueryRowContext(ctx, `
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
	rows, err := db.reader().QueryContext(ctx, `
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
	err := db.reader().QueryRowContext(ctx, `SELECT avatar_picture_id FROM chats WHERE id = ?`, chatID).Scan(&picID)
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
		INSERT INTO chats (id, name, name_source, last_message, last_message_time, last_message_direction, last_message_status, unread_count, is_group, is_pinned, pinned_order, is_archived, is_muted, mute_end_timestamp, history_exhausted, avatar_local_path, avatar_picture_id)
		SELECT ?, name, name_source, last_message, last_message_time, last_message_direction, last_message_status, unread_count, is_group, is_pinned, pinned_order, is_archived, is_muted, mute_end_timestamp, history_exhausted, avatar_local_path, avatar_picture_id
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
			is_pinned = MAX(chats.is_pinned, excluded.is_pinned),
			pinned_order = MAX(chats.pinned_order, excluded.pinned_order),
			is_archived = MAX(chats.is_archived, excluded.is_archived),
			is_muted = MAX(chats.is_muted, excluded.is_muted),
			mute_end_timestamp = MAX(chats.mute_end_timestamp, excluded.mute_end_timestamp),
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
