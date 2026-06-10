package store

import (
	"context"
	"time"
)

func (db *DB) ensureGroupParticipantsTable(ctx context.Context) error {
	_, err := db.conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS group_participants (
			chat_id TEXT NOT NULL,
			jid TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (chat_id, jid)
		)
	`)
	return err
}

// ReplaceGroupParticipants swaps the stored member set for a group with the
// authoritative list from a GetGroupInfo fetch.
func (db *DB) ReplaceGroupParticipants(ctx context.Context, chatID string, jids []string) error {
	if chatID == "" {
		return nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM group_participants WHERE chat_id = ?`, chatID); err != nil {
		return err
	}

	now := time.Now().Unix()
	for _, jid := range jids {
		if jid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO group_participants (chat_id, jid, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(chat_id, jid) DO NOTHING
		`, chatID, jid, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// AddGroupParticipants applies incremental joins from group info events.
func (db *DB) AddGroupParticipants(ctx context.Context, chatID string, jids []string) error {
	if chatID == "" || len(jids) == 0 {
		return nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	for _, jid := range jids {
		if jid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO group_participants (chat_id, jid, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(chat_id, jid) DO UPDATE SET updated_at = excluded.updated_at
		`, chatID, jid, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RemoveGroupParticipants applies incremental leaves from group info events.
func (db *DB) RemoveGroupParticipants(ctx context.Context, chatID string, jids []string) error {
	if chatID == "" || len(jids) == 0 {
		return nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, jid := range jids {
		if jid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM group_participants WHERE chat_id = ? AND jid = ?
		`, chatID, jid); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *DB) ListGroupParticipants(ctx context.Context, chatID string) ([]string, error) {
	rows, err := db.reader().QueryContext(ctx, `
		SELECT jid FROM group_participants WHERE chat_id = ? ORDER BY jid ASC
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jids := make([]string, 0, 16)
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		jids = append(jids, jid)
	}
	return jids, rows.Err()
}
