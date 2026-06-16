package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetDaemonConfig returns the stored value for key, or "" (no error) when the
// key has never been set. Mirrors GetAppStateValue but lives in the separate
// daemon_config table reserved for user preferences.
func (db *DB) GetDaemonConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := db.reader().QueryRowContext(ctx, `SELECT value FROM daemon_config WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetDaemonConfig upserts a user-preference key/value pair.
func (db *DB) SetDaemonConfig(ctx context.Context, key, value string) error {
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO daemon_config (key, value, updated_at) VALUES (?, ?, unixepoch())
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch()
	`, key, value)
	return err
}
