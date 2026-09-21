package store

import (
	"context"
	"time"
)

// Channel is one followed WhatsApp channel (newsletter): directory facts
// cached from GetSubscribedNewsletters. Message bodies are fetched live and
// never stored.
type Channel struct {
	ID          string
	Name        string
	Description string
	Followers   int
	Verified    bool
	Muted       bool
}

// SaveChannels replaces the subscribed-channel cache with a fresh listing.
func (db *DB) SaveChannels(ctx context.Context, channels []Channel) error {
	defer db.timeOp("SaveChannels", time.Now())
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM channels`); err != nil {
		return err
	}
	for _, channel := range channels {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO channels (id, name, description, followers, verified, muted)
			VALUES (?, ?, ?, ?, ?, ?)
		`, channel.ID, channel.Name, channel.Description, channel.Followers, boolToInt(channel.Verified), boolToInt(channel.Muted)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListChannels returns followed channels, most-followed first.
func (db *DB) ListChannels(ctx context.Context) ([]Channel, error) {
	defer db.timeOp("ListChannels", time.Now())
	rows, err := db.reader().QueryContext(ctx, `SELECT id, name, description, followers, verified, muted FROM channels ORDER BY followers DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := []Channel{}
	for rows.Next() {
		var channel Channel
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Description, &channel.Followers, &channel.Verified, &channel.Muted); err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return channels, nil
}

// SetChannelMuted flips the cached mute flag (source of truth refreshed on
// the next channels.refresh).
func (db *DB) SetChannelMuted(ctx context.Context, id string, muted bool) error {
	defer db.timeOp("SetChannelMuted", time.Now())
	_, err := db.conn.ExecContext(ctx, `UPDATE channels SET muted = ? WHERE id = ?`, boolToInt(muted), id)
	return err
}
