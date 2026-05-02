package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
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

	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS app_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE IF NOT EXISTS chats (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			last_message TEXT NOT NULL DEFAULT '',
			last_message_time INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			is_group INTEGER NOT NULL DEFAULT 0
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
	}

	for _, statement := range statements {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	if err := db.ensureMessageReadColumn(ctx); err != nil {
		return err
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
