package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mattn/go-sqlite3"
)

const schemaVersion = 7
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
	// slowOp, when set, is called with the name and duration of store
	// operations that exceed slowOpThreshold (pool wait + exec time), so
	// writer-connection contention is visible in the daemon log.
	slowOp func(op string, d time.Duration)
	// selfJID caches the account's own jid so a poll tally can mark our own
	// vote without a daemon_config read on every page of messages. It changes
	// once per login.
	selfJID atomic.Pointer[string]
}

const slowOpThreshold = 100 * time.Millisecond

// SetSlowOpLogger installs a callback invoked for store operations slower
// than slowOpThreshold. Pass nil to disable.
func (db *DB) SetSlowOpLogger(f func(op string, d time.Duration)) {
	db.slowOp = f
}

// timeOp reports an operation to the slow-op logger if it ran long. Use as
// `defer db.timeOp("Name", time.Now())`.
func (db *DB) timeOp(op string, start time.Time) {
	if db.slowOp == nil {
		return
	}
	if d := time.Since(start); d >= slowOpThreshold {
		db.slowOp(op, d)
	}
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
	db.loadSelfJID(ctx)

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
		// Daemon-side user preferences (notifications, media auto-download) that
		// are not part of the WhatsApp account. Distinct from app_state, which
		// holds internal sync bookkeeping. Read/written via daemon_config.go.
		`CREATE TABLE IF NOT EXISTS daemon_config (
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
			pinned_order INTEGER NOT NULL DEFAULT 0,
			is_favorite INTEGER NOT NULL DEFAULT 0,
			is_archived INTEGER NOT NULL DEFAULT 0,
			is_muted INTEGER NOT NULL DEFAULT 0,
			mute_end_timestamp INTEGER NOT NULL DEFAULT 0,
			history_exhausted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			sender_id TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL,
			sort_ms INTEGER NOT NULL DEFAULT 0,
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
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_sort ON messages(chat_id, sort_ms DESC, id DESC)`,
		// Deleting a message for me removes the row, and a later backfill chunk
		// would put it straight back. The id outlives the row so the message
		// stays deleted.
		`CREATE TABLE IF NOT EXISTS deleted_messages (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL DEFAULT '',
			deleted_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp_id ON messages(chat_id, timestamp DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_read_candidates ON messages(chat_id, direction, is_read, timestamp ASC, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_sender_chat ON messages(sender_id, chat_id)`,
		`CREATE TABLE IF NOT EXISTS message_reactions (
			message_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			emoji TEXT NOT NULL,
			sender_name TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL DEFAULT 0,
			from_me INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, sender_id),
			FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_message_reactions_message ON message_reactions(message_id)`,
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
		// Contact statuses (stories) live outside chats: storing them as chat
		// messages would materialize a bogus "status" chat row.
		`CREATE TABLE IF NOT EXISTS status_updates (
			id TEXT PRIMARY KEY,
			sender_id TEXT NOT NULL,
			sender_name TEXT NOT NULL DEFAULT '',
			timestamp INTEGER NOT NULL,
			kind TEXT NOT NULL DEFAULT 'text',
			text TEXT NOT NULL DEFAULT '',
			text_bg INTEGER NOT NULL DEFAULT 0,
			text_font INTEGER NOT NULL DEFAULT 0,
			media_mime_type TEXT NOT NULL DEFAULT '',
			media_kind TEXT NOT NULL DEFAULT '',
			media_local_path TEXT NOT NULL DEFAULT '',
			media_thumbnail_local_path TEXT NOT NULL DEFAULT '',
			media_width INTEGER NOT NULL DEFAULT 0,
			media_height INTEGER NOT NULL DEFAULT 0,
			media_payload BLOB NOT NULL DEFAULT x'',
			media_duration_secs INTEGER NOT NULL DEFAULT 0,
			media_size_bytes INTEGER NOT NULL DEFAULT 0,
			media_file_name TEXT NOT NULL DEFAULT '',
			is_viewed INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_status_updates_timestamp ON status_updates(timestamp DESC)`,
		`CREATE TABLE IF NOT EXISTS status_viewers (
			status_id TEXT NOT NULL,
			viewer_jid TEXT NOT NULL,
			viewed_at INTEGER NOT NULL,
			PRIMARY KEY (status_id, viewer_jid)
		)`,
		// Previous bodies of edited messages, oldest first. The live row
		// always holds the current version; this table is the edit history.
		// The generated id orders versions: two edits in the same
		// millisecond must both survive.
		`CREATE TABLE IF NOT EXISTS message_edits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT NOT NULL,
			edited_at_millis INTEGER NOT NULL,
			text TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_message_edits_message ON message_edits(message_id, id)`,
		// Contacts whose expired statuses are kept instead of hidden: the
		// Status tab shows only unexpired statuses by default, and kept
		// contacts grow an archived section with their older ones.
		`CREATE TABLE IF NOT EXISTS status_keep_senders (
			sender_id TEXT PRIMARY KEY,
			kept_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		// Contacts whose statuses are hidden from the main Status tab into a
		// collapsed Muted section. Mirrors the phone's muted-status list.
		`CREATE TABLE IF NOT EXISTS status_muted_senders (
			sender_id TEXT PRIMARY KEY,
			muted_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE IF NOT EXISTS scheduled_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			text TEXT NOT NULL,
			send_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE INDEX IF NOT EXISTS idx_scheduled_messages_send_at ON scheduled_messages(send_at, id)`,
		// Named chat folders with per-chat assignment (chats.folder_id).
		`CREATE TABLE IF NOT EXISTS chat_folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		// Followed channels (newsletters): directory rows; message content
		// stays server-side behind the channels view.
		`CREATE TABLE IF NOT EXISTS channels (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			followers INTEGER NOT NULL DEFAULT 0,
			verified INTEGER NOT NULL DEFAULT 0,
			muted INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
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
	if version < 6 {
		// v6: repair avatar rows poisoned by the old nil-info bug (a fetch
		// that never downloaded anything was cached as "available" for a full
		// TTL). Forcing next_check_at to 0 costs one cheap ExistingID
		// re-verification per row under the new TTL rules.
		if _, err := db.conn.ExecContext(ctx, `UPDATE avatars SET next_check_at = 0 WHERE status = ''`); err != nil {
			return err
		}
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

	if err := db.ensurePayloadColumns(ctx); err != nil {
		return err
	}
	if err := db.ensureRichMessageTables(ctx); err != nil {
		return err
	}
	if err := db.ensureSenderDeviceColumn(ctx); err != nil {
		return err
	}
	if err := db.ensureMessageEditsTable(ctx); err != nil {
		return err
	}
	if err := db.ensureChatFolderColumns(ctx); err != nil {
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

	if err := db.ensureMessageRevokedColumn(ctx); err != nil {
		return err
	}
	if err := db.ensureMessageReceiptsTable(ctx); err != nil {
		return err
	}
	if err := db.ensureGroupParticipantsTable(ctx); err != nil {
		return err
	}
	if err := db.ensureMessageSearchIndex(ctx); err != nil {
		return err
	}

	if version < 7 {
		// v7: repair chats whose badge was written without any unread message
		// rows behind it (history sync inserts with CountUnread=false, and the
		// phone's "mark unread" is a dot). Such a badge could never be cleared:
		// mark-read only ever looks at is_read=0 rows and found none. Needs the
		// is_revoked column, hence its position after every ensure* step.
		if err := db.repairChatUnreadState(ctx); err != nil {
			return err
		}
	}

	return nil
}

// repairChatUnreadState reconstructs per-message read state for chats carrying
// a non-zero badge with no unread rows behind it, so the badge becomes both
// clearable and receipt-able. OverwriteChatUnreadCount is the same reconcile
// history sync now performs, so the repaired rows match what a fresh sync
// would have produced.
func (db *DB) repairChatUnreadState(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT c.id, c.unread_count
		FROM chats c
		WHERE c.unread_count > 0
		  AND NOT EXISTS (
			SELECT 1 FROM messages m
			WHERE m.chat_id = c.id AND m.direction = ? AND m.is_read = 0
		  )
	`, DirectionIncoming)
	if err != nil {
		return err
	}
	type stale struct {
		id     string
		unread int32
	}
	var chats []stale
	for rows.Next() {
		var entry stale
		if err := rows.Scan(&entry.id, &entry.unread); err != nil {
			rows.Close()
			return err
		}
		chats = append(chats, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, entry := range chats {
		if _, _, err := db.OverwriteChatUnreadCount(ctx, entry.id, uint32(entry.unread)); err != nil {
			return err
		}
	}
	return nil
}

// ensureMessageSearchIndex creates the FTS5 full-text index over messages.text
// and the triggers that keep it in sync with inserts, deletes, and text edits.
// It is an external-content index (content='messages'): the index stores only
// the inverted terms and reads the text back from the messages table by rowid,
// so it adds little storage. Requires the sqlite driver to be built with FTS5
// (the daemon is built with -tags sqlite_fts5).
func (db *DB) ensureMessageSearchIndex(ctx context.Context) error {
	var existed int
	if err := db.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'messages_fts'`,
	).Scan(&existed); err != nil {
		return err
	}

	for _, statement := range []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			text,
			content='messages',
			content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		)`,
		`CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, text) VALUES (new.rowid, new.text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, text) VALUES('delete', old.rowid, old.text);
		END`,
		// Only reindex when the text actually changed; star/pin/read updates
		// touch the row without altering its searchable content.
		`CREATE TRIGGER IF NOT EXISTS messages_fts_au AFTER UPDATE ON messages WHEN old.text IS NOT new.text BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, text) VALUES('delete', old.rowid, old.text);
			INSERT INTO messages_fts(rowid, text) VALUES (new.rowid, new.text);
		END`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	// Newly created on a database that already holds messages: backfill the
	// index from existing rows. (On a fresh DB this is a cheap no-op.)
	if existed == 0 {
		if _, err := db.conn.ExecContext(ctx, `INSERT INTO messages_fts(messages_fts) VALUES('rebuild')`); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) ensureMessageRevokedColumn(ctx context.Context) error {
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
		{"is_revoked", `ALTER TABLE messages ADD COLUMN is_revoked INTEGER NOT NULL DEFAULT 0`},
		{"is_forwarded", `ALTER TABLE messages ADD COLUMN is_forwarded INTEGER NOT NULL DEFAULT 0`},
		{"is_edited", `ALTER TABLE messages ADD COLUMN is_edited INTEGER NOT NULL DEFAULT 0`},
		{"is_starred", `ALTER TABLE messages ADD COLUMN is_starred INTEGER NOT NULL DEFAULT 0`},
		{"pinned_at", `ALTER TABLE messages ADD COLUMN pinned_at INTEGER NOT NULL DEFAULT 0`},
		{"pinned_until", `ALTER TABLE messages ADD COLUMN pinned_until INTEGER NOT NULL DEFAULT 0`},
		// Newline-joined full JIDs of @-mentioned participants (see
		// encodeMentionedJIDs). Empty for the vast majority of messages.
		{"mentioned_jids", `ALTER TABLE messages ADD COLUMN mentioned_jids TEXT NOT NULL DEFAULT ''`},
		{"sort_ms", `ALTER TABLE messages ADD COLUMN sort_ms INTEGER NOT NULL DEFAULT 0`},
	}
	for _, a := range alterations {
		if existing[a.col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, a.def); err != nil {
			return fmt.Errorf("add messages.%s: %w", a.col, err)
		}
	}

	// Rows written before sort_ms existed carry a zero, which would sort them
	// all above everything. Seconds are the best precision those rows ever had,
	// and the message id still breaks their ties deterministically.
	if _, err := db.conn.ExecContext(ctx, `UPDATE messages SET sort_ms = timestamp * 1000 WHERE sort_ms = 0`); err != nil {
		return fmt.Errorf("backfill messages.sort_ms: %w", err)
	}

	// These indexes reference is_starred / pinned_until, so they must be created
	// after the ALTERs above add those columns (ensureQueryIndexes runs earlier).
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_starred ON messages(chat_id, is_starred, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_pinned ON messages(chat_id, pinned_until)`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
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
		{"is_favorite", `ALTER TABLE chats ADD COLUMN is_favorite INTEGER NOT NULL DEFAULT 0`},
		{"is_archived", `ALTER TABLE chats ADD COLUMN is_archived INTEGER NOT NULL DEFAULT 0`},
		{"is_muted", `ALTER TABLE chats ADD COLUMN is_muted INTEGER NOT NULL DEFAULT 0`},
		{"mute_end_timestamp", `ALTER TABLE chats ADD COLUMN mute_end_timestamp INTEGER NOT NULL DEFAULT 0`},
		{"history_exhausted", `ALTER TABLE chats ADD COLUMN history_exhausted INTEGER NOT NULL DEFAULT 0`},
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
		{"media_download_error", `ALTER TABLE messages ADD COLUMN media_download_error TEXT NOT NULL DEFAULT ''`},
		{"media_payload", `ALTER TABLE messages ADD COLUMN media_payload BLOB NOT NULL DEFAULT x''`},
		{"media_cache_key", `ALTER TABLE messages ADD COLUMN media_cache_key TEXT NOT NULL DEFAULT ''`},
		{"media_duration_secs", `ALTER TABLE messages ADD COLUMN media_duration_secs INTEGER NOT NULL DEFAULT 0`},
		{"media_size_bytes", `ALTER TABLE messages ADD COLUMN media_size_bytes INTEGER NOT NULL DEFAULT 0`},
		{"media_file_name", `ALTER TABLE messages ADD COLUMN media_file_name TEXT NOT NULL DEFAULT ''`},
		{"media_page_count", `ALTER TABLE messages ADD COLUMN media_page_count INTEGER NOT NULL DEFAULT 0`},
		{"media_waveform", `ALTER TABLE messages ADD COLUMN media_waveform BLOB NOT NULL DEFAULT x''`},
		{"media_played", `ALTER TABLE messages ADD COLUMN media_played INTEGER NOT NULL DEFAULT 0`},
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

// existingColumns reads the column names of one table. Every ensure*Columns
// step needs this before it can decide what to ALTER.
func (db *DB) existingColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := db.conn.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		existing[name] = true
	}
	return existing, rows.Err()
}

// addColumns applies the ALTER statements whose column is not there yet. The
// statements are written in full rather than generated so a reader of this file
// sees exactly the DDL that runs.
func (db *DB) addColumns(ctx context.Context, table string, alterations [][2]string) error {
	existing, err := db.existingColumns(ctx, table)
	if err != nil {
		return err
	}
	for _, alteration := range alterations {
		col, statement := alteration[0], alteration[1]
		if existing[col] {
			continue
		}
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, col, err)
		}
	}
	return nil
}

// ensurePayloadColumns adds the per-kind payload columns that let a message
// carry something structured without growing a column per kind. payload_summary
// is separate from payload_json so the chat-list preview and the wire fallback
// never parse JSON to render one line.
func (db *DB) ensurePayloadColumns(ctx context.Context) error {
	if err := db.addColumns(ctx, "messages", [][2]string{
		{"payload_json", `ALTER TABLE messages ADD COLUMN payload_json TEXT NOT NULL DEFAULT ''`},
		{"payload_summary", `ALTER TABLE messages ADD COLUMN payload_summary TEXT NOT NULL DEFAULT ''`},
		{"album_parent_id", `ALTER TABLE messages ADD COLUMN album_parent_id TEXT NOT NULL DEFAULT ''`},
		{"album_index", `ALTER TABLE messages ADD COLUMN album_index INTEGER NOT NULL DEFAULT 0`},
		{"is_kept", `ALTER TABLE messages ADD COLUMN is_kept INTEGER NOT NULL DEFAULT 0`},
		// is_view_once marks our own view-once sends (inbound view-once is a
		// phone-only tombstone, never media rows).
		{"is_view_once", `ALTER TABLE messages ADD COLUMN is_view_once INTEGER NOT NULL DEFAULT 0`},
	}); err != nil {
		return err
	}
	// Album children are excluded from every transcript query by a correlated
	// lookup on this column, so it needs to be cheap to ask "is this row in an
	// album" and "give me this album's children in order".
	_, err := db.conn.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_messages_album_parent
		ON messages(album_parent_id, album_index)
		WHERE album_parent_id != ''
	`)
	return err
}

// ensureRichMessageTables creates the side tables for the kinds whose state is
// mutated after the message lands (poll tallies, live-location trails, event
// RSVPs). Everything static enough to be written once lives in payload_json
// instead.
func (db *DB) ensureRichMessageTables(ctx context.Context) error {
	statements := []string{
		// A poll's options, in wire order. sha256 is what a decrypted vote
		// names, so votes are matched back to options by hash, never by text.
		`CREATE TABLE IF NOT EXISTS poll_options (
			message_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			name TEXT NOT NULL,
			sha256 BLOB NOT NULL,
			PRIMARY KEY (message_id, idx),
			FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_poll_options_hash ON poll_options(message_id, sha256)`,
		// One row per (poll, voter, chosen option). A vote message carries a
		// voter's entire current selection rather than a delta, so applying one
		// deletes that voter's rows and re-inserts them.
		`CREATE TABLE IF NOT EXISTS poll_votes (
			message_id TEXT NOT NULL,
			voter_jid TEXT NOT NULL,
			option_sha BLOB NOT NULL,
			voted_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, voter_jid, option_sha),
			FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
		// When each voter last answered, kept whether or not they currently
		// have a selection. The vote rows alone cannot carry this: withdrawing
		// a vote deletes them, and then a redelivered older vote has nothing to
		// look stale against and resurrects an answer the voter took back.
		// WhatsApp redelivers on every reconnect, so this is routine.
		`CREATE TABLE IF NOT EXISTS poll_voters (
			message_id TEXT NOT NULL,
			voter_jid TEXT NOT NULL,
			voted_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, voter_jid),
			FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
		// Votes that arrived before the poll they vote on: history sync does not
		// promise ordering, and a resend can outrun its original. No foreign key
		// here precisely because the poll row does not exist yet.
		`CREATE TABLE IF NOT EXISTS poll_votes_pending (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			poll_message_id TEXT NOT NULL,
			voter_jid TEXT NOT NULL,
			enc_payload BLOB NOT NULL,
			enc_iv BLOB NOT NULL,
			sender_ts INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE INDEX IF NOT EXISTS idx_poll_votes_pending_poll ON poll_votes_pending(poll_message_id)`,
		// One open live-location share. whatsmeow has no concept of these, so
		// the correlation between an opening LocationMessage and the
		// LiveLocationMessage updates that follow is entirely ours.
		`CREATE TABLE IF NOT EXISTS live_location_shares (
			message_id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			started_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL DEFAULT 0,
			last_seq INTEGER NOT NULL DEFAULT 0,
			last_update_at INTEGER NOT NULL DEFAULT 0,
			ended INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_live_shares_open ON live_location_shares(chat_id, ended, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_live_shares_sender ON live_location_shares(chat_id, sender_id, ended)`,
		// The trail. Ordered and deduped by the sender's sequence number, so a
		// late or replayed update never drags the pin backwards.
		`CREATE TABLE IF NOT EXISTS live_location_points (
			message_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			ts INTEGER NOT NULL,
			lat REAL NOT NULL,
			lng REAL NOT NULL,
			accuracy_m INTEGER NOT NULL DEFAULT 0,
			speed_mps REAL NOT NULL DEFAULT 0,
			heading_deg INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, seq),
			FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
		// One RSVP per responder, replaced when they change their mind.
		`CREATE TABLE IF NOT EXISTS event_responses (
			message_id TEXT NOT NULL,
			responder_jid TEXT NOT NULL,
			response TEXT NOT NULL,
			extra_guests INTEGER NOT NULL DEFAULT 0,
			ts INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, responder_jid),
			FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
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

// ensureMessageEditsTable rebuilds message_edits with an autoincrement id
// when it still has the first-run schema keyed by (message_id, edited_at),
// under which two edits in the same millisecond collided and lost one.
// Fresh databases already get the new schema from the CREATE TABLE above.
func (db *DB) ensureMessageEditsTable(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(message_edits)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	exists, hasID := false, false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		exists = true
		if name == "id" {
			hasID = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !exists || hasID {
		return nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE message_edits_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT NOT NULL,
			edited_at_millis INTEGER NOT NULL,
			text TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO message_edits_new (message_id, edited_at_millis, text)
			SELECT message_id, edited_at, text FROM message_edits`,
		`DROP TABLE message_edits`,
		`ALTER TABLE message_edits_new RENAME TO message_edits`,
		`CREATE INDEX IF NOT EXISTS idx_message_edits_message ON message_edits(message_id, id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rebuild message_edits: %w", err)
		}
	}
	return tx.Commit()
}

// ensureSenderDeviceColumn adds messages.sender_device (0 = primary phone
// app, >0 = linked device) for databases created before the sender-client
// indicator existed.
func (db *DB) ensureSenderDeviceColumn(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "sender_device" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := db.conn.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN sender_device INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add messages.sender_device: %w", err)
	}
	return nil
}

// ensureChatFolderColumns adds chats.folder_id for databases created before
// custom chat folders existed.
func (db *DB) ensureChatFolderColumns(ctx context.Context) error {
	_, err := db.conn.ExecContext(ctx, `ALTER TABLE chats ADD COLUMN folder_id INTEGER REFERENCES chat_folders(id) ON DELETE SET NULL`)
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("add chats.folder_id: %w", err)
	}
	return nil
}
