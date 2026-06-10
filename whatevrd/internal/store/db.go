package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/mattn/go-sqlite3"
)

const schemaVersion = 3
const SQLiteDriverName = "whatevrd-sqlite"

// SQLiteReadDriverName backs the read-only connection pool. Its ConnectHook
// applies the connection-level pragmas to every physical connection (a pool of
// many connections cannot be configured with one-off ExecContext PRAGMAs, since
// those only land on whichever connection is currently checked out).
const SQLiteReadDriverName = "whatevrd-sqlite-ro"

type DB struct {
	// conn is the single writer connection. SQLite allows only one writer, and
	// keeping MaxOpenConns(1) here both serializes writes (no SQLITE_BUSY) and
	// avoids the *sql.Tx-holds-the-only-connection deadlock footgun.
	conn *sql.DB
	// readConn is a separate WAL reader pool. Under WAL a reader never blocks
	// the writer (or vice-versa), so hot read paths (e.g. the sticker picker's
	// per-tile GetSticker burst) no longer queue head-of-line behind a long
	// history-sync write on the lone writer connection.
	readConn *sql.DB
}

// reader returns the connection pool to use for read-only queries. It falls
// back to the writer connection if the reader pool was not opened, so a DB
// value built outside Open() still functions.
func (db *DB) reader() *sql.DB {
	if db.readConn != nil {
		return db.readConn
	}
	return db.conn
}

func init() {
	sql.Register(SQLiteDriverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return conn.RegisterFunc("chat_name_source_priority", sqliteChatNameSourcePriority, true)
		},
	})
	sql.Register(SQLiteReadDriverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			if err := conn.RegisterFunc("chat_name_source_priority", sqliteChatNameSourcePriority, true); err != nil {
				return err
			}
			// query_only guards the pool against accidental writes; the rest
			// mirror the writer connection (mmap off to keep RSS down).
			for _, pragma := range []string{
				`PRAGMA busy_timeout = 5000`,
				`PRAGMA query_only = ON`,
				`PRAGMA foreign_keys = ON`,
				`PRAGMA mmap_size = 0`,
			} {
				if _, err := conn.Exec(pragma, nil); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

func sqliteChatNameSourcePriority(value any) int64 {
	switch value := value.(type) {
	case string:
		return int64(chatNameSourcePriority(value))
	case []byte:
		return int64(chatNameSourcePriority(string(value)))
	default:
		return int64(chatNameSourcePriority(""))
	}
}

func Open(ctx context.Context, path string) (*DB, error) {
	conn, err := sql.Open(SQLiteDriverName, path)
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

	// migrate() has put the file in WAL mode, so the reader pool below sees
	// committed writes immediately and runs concurrently with the writer. The
	// RO driver's ConnectHook applies the per-connection pragmas to every
	// connection in the pool.
	readConn, err := sql.Open(SQLiteReadDriverName, path)
	if err != nil {
		conn.Close()
		return nil, err
	}
	readConn.SetMaxOpenConns(4)
	readConn.SetMaxIdleConns(4)
	if err := readConn.PingContext(ctx); err != nil {
		readConn.Close()
		conn.Close()
		return nil, err
	}
	db.readConn = readConn

	return db, nil
}

func (db *DB) Close() error {
	var readErr error
	if db.readConn != nil {
		readErr = db.readConn.Close()
	}
	if err := db.conn.Close(); err != nil {
		return err
	}
	return readErr
}

func (db *DB) migrate(ctx context.Context) error {
	for _, statement := range []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		// NORMAL is durable for the database itself under WAL: a power loss
		// can only drop the most recent commits, never corrupt the file.
		// FULL would fsync every commit, which dominates history-sync writes.
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
		// Disable mmap so DB pages are not faulted into process RSS; reads go
		// through pread() and the kernel page cache instead. With a large DB an
		// mmap would inflate RSS by hundreds of MB for little read benefit. The
		// default ~2MB page cache and on-disk temp store are likewise kept to
		// hold steady-state memory down.
		`PRAGMA mmap_size = 0`,
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
			reply_to_message_id TEXT NOT NULL DEFAULT '',
			reply_to_sender_id TEXT NOT NULL DEFAULT '',
			reply_to_sender_name TEXT NOT NULL DEFAULT '',
			reply_to_text TEXT NOT NULL DEFAULT '',
			reply_to_media_kind TEXT NOT NULL DEFAULT '',
			reply_to_media_mime_type TEXT NOT NULL DEFAULT '',
			reply_to_direction TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(chat_id) REFERENCES chats(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp ON messages(chat_id, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp_id ON messages(chat_id, timestamp DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_read_candidates ON messages(chat_id, direction, is_read, timestamp ASC, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_sender_chat ON messages(sender_id, chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_chats_last_message_time ON chats(last_message_time DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_chats_list_order ON chats((CASE WHEN is_pinned != 0 THEN 0 ELSE 1 END), pinned_order DESC, last_message_time DESC, id ASC)`,
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

	if err := db.ensureChatUpdatedAtColumn(ctx); err != nil {
		return err
	}

	if err := db.ensureMediaColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureSendRetryColumns(ctx); err != nil {
		return err
	}
	if err := db.ensureReplyColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureHistorySyncColumns(ctx); err != nil {
		return err
	}

	if err := db.ensureUndecryptableMessagesTable(ctx); err != nil {
		return err
	}
	if err := db.ensureQueryIndexes(ctx); err != nil {
		return err
	}
	if err := db.ensureStickerCacheKeys(ctx); err != nil {
		return err
	}
	if err := db.ensureStickerTables(ctx); err != nil {
		return err
	}

	return nil
}

func (db *DB) ensureQueryIndexes(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_messages_pending_outgoing ON messages(direction, status, next_send_attempt, timestamp ASC, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_downloaded_stickers ON messages(media_kind, media_local_path)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_sticker_cache_key ON messages(media_kind, media_cache_key, media_local_path)`,
		`CREATE INDEX IF NOT EXISTS idx_history_sync_chunks_prune ON history_sync_chunks(status, updated_at)`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
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

// ensureChatUpdatedAtColumn adds chats.updated_at and keeps it current via
// triggers, so every existing write path bumps the stamp without changes.
// Recursive triggers are off by default in SQLite, so the trigger's own
// UPDATE cannot re-fire it.
func (db *DB) ensureChatUpdatedAtColumn(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(chats)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasUpdatedAt := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "updated_at" {
			hasUpdatedAt = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if !hasUpdatedAt {
		if _, err := db.conn.ExecContext(ctx, `ALTER TABLE chats ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add chats.updated_at: %w", err)
		}
		if _, err := db.conn.ExecContext(ctx, `UPDATE chats SET updated_at = unixepoch()`); err != nil {
			return err
		}
	}

	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS trg_chats_updated_at_insert AFTER INSERT ON chats
		BEGIN
			UPDATE chats SET updated_at = unixepoch() WHERE id = NEW.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_chats_updated_at_update AFTER UPDATE ON chats
		WHEN NEW.updated_at = OLD.updated_at
		BEGIN
			UPDATE chats SET updated_at = unixepoch() WHERE id = NEW.id;
		END`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
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
		{"media_cache_key", `ALTER TABLE messages ADD COLUMN media_cache_key TEXT NOT NULL DEFAULT ''`},
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

func (db *DB) ensureReplyColumns(ctx context.Context) error {
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
		{"reply_to_message_id", `ALTER TABLE messages ADD COLUMN reply_to_message_id TEXT NOT NULL DEFAULT ''`},
		{"reply_to_sender_id", `ALTER TABLE messages ADD COLUMN reply_to_sender_id TEXT NOT NULL DEFAULT ''`},
		{"reply_to_sender_name", `ALTER TABLE messages ADD COLUMN reply_to_sender_name TEXT NOT NULL DEFAULT ''`},
		{"reply_to_text", `ALTER TABLE messages ADD COLUMN reply_to_text TEXT NOT NULL DEFAULT ''`},
		{"reply_to_media_kind", `ALTER TABLE messages ADD COLUMN reply_to_media_kind TEXT NOT NULL DEFAULT ''`},
		{"reply_to_media_mime_type", `ALTER TABLE messages ADD COLUMN reply_to_media_mime_type TEXT NOT NULL DEFAULT ''`},
		{"reply_to_direction", `ALTER TABLE messages ADD COLUMN reply_to_direction TEXT NOT NULL DEFAULT ''`},
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
