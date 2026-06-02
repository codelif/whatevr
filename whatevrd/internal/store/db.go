package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"

	"modernc.org/sqlite"
)

const schemaVersion = 3

type DB struct {
	conn *sql.DB
}

func init() {
	sqlite.MustRegisterScalarFunction("chat_name_source_priority", 1, func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if len(args) == 0 {
			return int64(chatNameSourcePriority("")), nil
		}
		value, _ := args[0].(string)
		return int64(chatNameSourcePriority(value)), nil
	})
}

func Open(ctx context.Context, path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)

	db := &DB{conn: conn}
	if err := db.migrate(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	if err := db.CheckIntegrity(ctx); err != nil {
		conn.Close()
		return nil, err
	}

	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate(ctx context.Context) error {
	for _, statement := range []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	version, err := db.userVersion(ctx)
	if err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, schemaVersion)
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS app_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE IF NOT EXISTS chats (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			name_source TEXT NOT NULL DEFAULT '',
			last_message TEXT NOT NULL DEFAULT '',
			last_message_time INTEGER NOT NULL DEFAULT 0,
			last_message_direction TEXT NOT NULL DEFAULT '',
			last_message_status TEXT NOT NULL DEFAULT '',
			unread_count INTEGER NOT NULL DEFAULT 0,
			is_group INTEGER NOT NULL DEFAULT 0,
			is_pinned INTEGER NOT NULL DEFAULT 0,
			pinned_order INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			sender_id TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL,
			direction TEXT NOT NULL,
			is_read INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL,
			FOREIGN KEY(chat_id) REFERENCES chats(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp ON messages(chat_id, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_chats_last_message_time ON chats(last_message_time DESC)`,
		`CREATE TABLE IF NOT EXISTS senders (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			avatar_local_path TEXT NOT NULL DEFAULT '',
			avatar_picture_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS history_sync_chunks (
			id TEXT PRIMARY KEY,
			sync_type INTEGER NOT NULL,
			chunk_order INTEGER NOT NULL DEFAULT 0,
			progress INTEGER NOT NULL DEFAULT 0,
			file_length INTEGER NOT NULL DEFAULT 0,
			direct_path TEXT NOT NULL DEFAULT '',
			media_key BLOB NOT NULL DEFAULT x'',
			file_sha256 BLOB NOT NULL DEFAULT x'',
			file_enc_sha256 BLOB NOT NULL DEFAULT x'',
			enc_handle TEXT NOT NULL DEFAULT '',
			inline_payload BLOB NOT NULL DEFAULT x'',
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE IF NOT EXISTS undecryptable_messages (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			sender_id TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE INDEX IF NOT EXISTS idx_undecryptable_messages_created_at ON undecryptable_messages(created_at)`,
	}

	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if err := db.ensureMessageReadColumn(ctx); err != nil {
		return err
	}

	if err := db.ensureChatsAvatarColumns(ctx); err != nil {
		return err
	}
	if err := db.ensureSendersAvatarColumns(ctx); err != nil {
		return err
	}
	if err := db.ensureAvatarTable(ctx); err != nil {
		return err
	}

	if err := db.ensureChatSummaryColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureChatNameSourceColumn(ctx); err != nil {
		return err
	}

	if err := db.ensureChatPinColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureMediaColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureSendRetryColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureHistorySyncColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureUndecryptableMessagesTable(ctx); err != nil {
		return err
	}

	return nil
}

func (db *DB) ensureUndecryptableMessagesTable(ctx context.Context) error {
	if _, err := db.conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS undecryptable_messages (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			sender_id TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)
	`); err != nil {
		return err
	}
	_, err := db.conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_undecryptable_messages_created_at ON undecryptable_messages(created_at)`)
	return err
}

func (db *DB) ensureAvatarTable(ctx context.Context) error {
	if _, err := db.conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS avatars (
			subject_kind TEXT NOT NULL,
			subject_id TEXT NOT NULL,
			fetch_jid TEXT NOT NULL DEFAULT '',
			picture_id TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			checked_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			next_check_at INTEGER NOT NULL DEFAULT 0,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (subject_kind, subject_id)
		)
	`); err != nil {
		return err
	}
	if _, err := db.conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_avatars_next_check ON avatars(next_check_at)`); err != nil {
		return err
	}
	if _, err := db.conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_avatars_fetch_jid ON avatars(fetch_jid)`); err != nil {
		return err
	}

	// Backfill once from legacy avatar columns. Future writes use avatars.
	if _, err := db.conn.ExecContext(ctx, `
		INSERT OR IGNORE INTO avatars (subject_kind, subject_id, fetch_jid, picture_id, local_path, status, checked_at, updated_at, next_check_at)
		SELECT CASE WHEN is_group = 1 THEN 'chat' ELSE 'chat' END, id, id, avatar_picture_id, avatar_local_path, avatar_status, avatar_checked_at, avatar_checked_at, 0
		FROM chats
		WHERE avatar_picture_id != '' OR avatar_local_path != '' OR avatar_status != ''
	`); err != nil {
		return err
	}
	_, err := db.conn.ExecContext(ctx, `
		INSERT OR IGNORE INTO avatars (subject_kind, subject_id, fetch_jid, picture_id, local_path, status, checked_at, updated_at, next_check_at)
		SELECT 'sender', id, id, avatar_picture_id, avatar_local_path, avatar_status, avatar_checked_at, avatar_checked_at, 0
		FROM senders
		WHERE avatar_picture_id != '' OR avatar_local_path != '' OR avatar_status != ''
	`)
	return err
}

func (db *DB) ensureHistorySyncColumns(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(history_sync_chunks)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasFileLength := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "file_length" {
			hasFileLength = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasFileLength {
		return nil
	}

	_, err = db.conn.ExecContext(ctx, `ALTER TABLE history_sync_chunks ADD COLUMN file_length INTEGER NOT NULL DEFAULT 0`)
	return err
}

func (db *DB) ensureChatNameSourceColumn(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(chats)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasNameSource := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "name_source" {
			hasNameSource = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasNameSource {
		return nil
	}

	if _, err := db.conn.ExecContext(ctx, `ALTER TABLE chats ADD COLUMN name_source TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add chats.name_source: %w", err)
	}
	_, err = db.conn.ExecContext(ctx, `
		UPDATE chats
		SET name_source = CASE
			WHEN name = id OR name LIKE '%@s.whatsapp.net' OR name LIKE '%@lid' THEN ?
			WHEN is_group = 1 THEN ?
			ELSE ''
		END
	`, ChatNameSourceRaw, ChatNameSourceGroup)
	return err
}

func (db *DB) ensureChatPinColumns(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(chats)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	alterations := []struct {
		col string
		def string
	}{
		{"is_pinned", `ALTER TABLE chats ADD COLUMN is_pinned INTEGER NOT NULL DEFAULT 0`},
		{"pinned_order", `ALTER TABLE chats ADD COLUMN pinned_order INTEGER NOT NULL DEFAULT 0`},
	}
	for _, a := range alterations {
		if existing[a.col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, a.def); err != nil {
			return fmt.Errorf("add chats.%s: %w", a.col, err)
		}
	}
	return nil
}

func (db *DB) userVersion(ctx context.Context) (int, error) {
	var version int
	if err := db.conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func (db *DB) CheckIntegrity(ctx context.Context) error {
	var result string
	if err := db.conn.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite integrity check failed: %s", result)
	}
	return nil
}

func (db *DB) Backup(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("backup path is required")
	}
	backupPath := filepath.ToSlash(path)
	_, err := db.conn.ExecContext(ctx, `VACUUM INTO ?`, backupPath)
	return err
}

func (db *DB) ensureChatsAvatarColumns(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(chats)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	alterations := []struct {
		col string
		def string
	}{
		{"avatar_local_path", `ALTER TABLE chats ADD COLUMN avatar_local_path TEXT NOT NULL DEFAULT ''`},
		{"avatar_picture_id", `ALTER TABLE chats ADD COLUMN avatar_picture_id TEXT NOT NULL DEFAULT ''`},
		{"avatar_status", `ALTER TABLE chats ADD COLUMN avatar_status TEXT NOT NULL DEFAULT ''`},
		{"avatar_checked_at", `ALTER TABLE chats ADD COLUMN avatar_checked_at INTEGER NOT NULL DEFAULT 0`},
	}
	for _, a := range alterations {
		if existing[a.col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, a.def); err != nil {
			return fmt.Errorf("add chats.%s: %w", a.col, err)
		}
	}
	return nil
}

func (db *DB) ensureSendersAvatarColumns(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(senders)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	alterations := []struct {
		col string
		def string
	}{
		{"avatar_status", `ALTER TABLE senders ADD COLUMN avatar_status TEXT NOT NULL DEFAULT ''`},
		{"avatar_checked_at", `ALTER TABLE senders ADD COLUMN avatar_checked_at INTEGER NOT NULL DEFAULT 0`},
	}
	for _, a := range alterations {
		if existing[a.col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, a.def); err != nil {
			return fmt.Errorf("add senders.%s: %w", a.col, err)
		}
	}
	return nil
}

func (db *DB) ensureChatSummaryColumns(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(chats)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	alterations := []struct {
		col string
		def string
	}{
		{"last_message_direction", `ALTER TABLE chats ADD COLUMN last_message_direction TEXT NOT NULL DEFAULT ''`},
		{"last_message_status", `ALTER TABLE chats ADD COLUMN last_message_status TEXT NOT NULL DEFAULT ''`},
	}
	for _, a := range alterations {
		if existing[a.col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, a.def); err != nil {
			return fmt.Errorf("add chats.%s: %w", a.col, err)
		}
	}

	_, err = db.conn.ExecContext(ctx, `
		UPDATE chats
		SET last_message_direction = COALESCE((
			SELECT direction
			FROM messages
			WHERE messages.chat_id = chats.id
			ORDER BY timestamp DESC, id DESC
			LIMIT 1
		), ''),
		last_message_status = COALESCE((
			SELECT status
			FROM messages
			WHERE messages.chat_id = chats.id
			ORDER BY timestamp DESC, id DESC
			LIMIT 1
		), '')
		WHERE last_message_time > 0
		  AND (last_message_direction = '' OR last_message_status = '')
	`)
	return err
}

func (db *DB) ensureMediaColumns(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	alterations := []struct {
		col string
		def string
	}{
		{"media_kind", `ALTER TABLE messages ADD COLUMN media_kind TEXT NOT NULL DEFAULT ''`},
		{"media_mime_type", `ALTER TABLE messages ADD COLUMN media_mime_type TEXT NOT NULL DEFAULT ''`},
		{"media_local_path", `ALTER TABLE messages ADD COLUMN media_local_path TEXT NOT NULL DEFAULT ''`},
		{"media_thumbnail_local_path", `ALTER TABLE messages ADD COLUMN media_thumbnail_local_path TEXT NOT NULL DEFAULT ''`},
		{"media_width", `ALTER TABLE messages ADD COLUMN media_width INTEGER NOT NULL DEFAULT 0`},
		{"media_height", `ALTER TABLE messages ADD COLUMN media_height INTEGER NOT NULL DEFAULT 0`},
		{"media_animated", `ALTER TABLE messages ADD COLUMN media_animated INTEGER NOT NULL DEFAULT 0`},
		{"media_payload", `ALTER TABLE messages ADD COLUMN media_payload BLOB NOT NULL DEFAULT x''`},
	}
	for _, a := range alterations {
		if existing[a.col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, a.def); err != nil {
			return fmt.Errorf("add messages.%s: %w", a.col, err)
		}
	}
	return nil
}

func (db *DB) ensureSendRetryColumns(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	alterations := []struct {
		col string
		def string
	}{
		{"send_attempts", `ALTER TABLE messages ADD COLUMN send_attempts INTEGER NOT NULL DEFAULT 0`},
		{"last_send_error", `ALTER TABLE messages ADD COLUMN last_send_error TEXT NOT NULL DEFAULT ''`},
		{"next_send_attempt", `ALTER TABLE messages ADD COLUMN next_send_attempt INTEGER NOT NULL DEFAULT 0`},
	}
	for _, a := range alterations {
		if existing[a.col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, a.def); err != nil {
			return fmt.Errorf("add messages.%s: %w", a.col, err)
		}
	}
	return nil
}

func (db *DB) ensureMessageReadColumn(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasReadColumn := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "is_read" {
			hasReadColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	if hasReadColumn {
		return nil
	}

	if _, err := db.conn.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN is_read INTEGER NOT NULL DEFAULT 1`); err != nil {
		return fmt.Errorf("add messages.is_read column: %w", err)
	}

	return nil
}
