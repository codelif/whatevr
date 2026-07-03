package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Reaction is a single reactor's emoji on a message. One reaction per sender
// per message; the latest one wins (an empty emoji removes it).
type Reaction struct {
	Emoji         string
	SenderID      string
	SenderName    string
	TimestampUnix int64
	FromMe        bool
}

// reactionQueryer is satisfied by both *sql.DB and *sql.Tx, so reactions can be
// loaded on the shared read connection or inside a write transaction.
type reactionQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// attachReactions loads every message's reactions in one batched query and
// fans them onto the slice (oldest reaction first), avoiding an N+1 over the
// page. Messages with no reactions keep a nil slice.
func attachReactions(ctx context.Context, q reactionQueryer, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}

	ids := make([]any, 0, len(messages))
	indexByID := make(map[string]int, len(messages))
	for i := range messages {
		messages[i].Reactions = nil
		ids = append(ids, messages[i].ID)
		indexByID[messages[i].ID] = i
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := q.QueryContext(ctx, `
		SELECT message_id, sender_id, emoji, sender_name, timestamp, from_me
		FROM message_reactions
		WHERE message_id IN (`+placeholders+`)
		ORDER BY timestamp ASC, sender_id ASC
	`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var messageID string
		var reaction Reaction
		var fromMe int
		if err := rows.Scan(&messageID, &reaction.SenderID, &reaction.Emoji, &reaction.SenderName, &reaction.TimestampUnix, &fromMe); err != nil {
			return err
		}
		reaction.FromMe = fromMe != 0
		if i, ok := indexByID[messageID]; ok {
			messages[i].Reactions = append(messages[i].Reactions, reaction)
		}
	}
	return rows.Err()
}

// attachReactionsOne attaches reactions to a single message in place.
func attachReactionsOne(ctx context.Context, q reactionQueryer, message *Message) error {
	batch := []Message{*message}
	if err := attachReactions(ctx, q, batch); err != nil {
		return err
	}
	message.Reactions = batch[0].Reactions
	return nil
}

// UpdateReactionSenderName backfills a reaction's display name, but never
// overwrites a name that is already set (live events remain authoritative).
func (db *DB) UpdateReactionSenderName(ctx context.Context, messageID, senderID, name string) error {
	if name == "" {
		return nil
	}
	_, err := db.conn.ExecContext(ctx, `
		UPDATE message_reactions SET sender_name = ?
		WHERE message_id = ? AND sender_id = ? AND sender_name = ''
	`, name, messageID, senderID)
	return err
}

// SaveReaction records (or, when emoji is empty, removes) one reactor's
// reaction on a message, then returns the reloaded target message with its full
// reaction set, the refreshed chat, and whether anything actually changed. The
// changed flag is false for no-op writes (same emoji re-sent, or a removal with
// nothing to remove) so callers can skip redundant MessageUpdated broadcasts.
//
// Returns sql.ErrNoRows when the target message is not in the store; reactions
// for messages we have not synced yet are simply dropped (they arrive with the
// message during history sync).
func (db *DB) SaveReaction(ctx context.Context, messageID, senderID, senderName, emoji string, timestampUnix int64, fromMe bool) (Message, Chat, bool, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	defer tx.Rollback()

	message, err := getMessageTx(ctx, tx, messageID)
	if err != nil {
		return Message{}, Chat{}, false, err
	}

	var existing string
	err = tx.QueryRowContext(ctx, `SELECT emoji FROM message_reactions WHERE message_id = ? AND sender_id = ?`, messageID, senderID).Scan(&existing)
	hadReaction := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Message{}, Chat{}, false, err
	}

	changed := false
	switch {
	case emoji == "":
		if hadReaction {
			if _, err := tx.ExecContext(ctx, `DELETE FROM message_reactions WHERE message_id = ? AND sender_id = ?`, messageID, senderID); err != nil {
				return Message{}, Chat{}, false, err
			}
			changed = true
		}
	case !hadReaction || existing != emoji:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_reactions (message_id, sender_id, emoji, sender_name, timestamp, from_me)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(message_id, sender_id) DO UPDATE SET
				emoji = excluded.emoji,
				sender_name = excluded.sender_name,
				timestamp = excluded.timestamp,
				from_me = excluded.from_me
		`, messageID, senderID, emoji, senderName, timestampUnix, boolToInt(fromMe)); err != nil {
			return Message{}, Chat{}, false, err
		}
		changed = true
	}

	updated, err := getMessageTx(ctx, tx, messageID)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	if err := attachReactionsOne(ctx, tx, &updated); err != nil {
		return Message{}, Chat{}, false, err
	}
	chat, err := getChatTx(ctx, tx, message.ChatID)
	if err != nil {
		return Message{}, Chat{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, Chat{}, false, err
	}
	return updated, chat, changed, nil
}
