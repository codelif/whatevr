package store

import (
	"context"
	"time"
)

const (
	ReceiptKindDelivered = "delivered"
	ReceiptKindRead      = "read"
	ReceiptKindPlayed    = "played"
)

// MessageReceipt is one participant's delivery/read state for one message.
// Timestamps are unix seconds; zero means "not yet".
type MessageReceipt struct {
	MessageID      string
	ChatID         string
	ParticipantJID string
	DeliveredTs    int64
	ReadTs         int64
	PlayedTs       int64
}

func (db *DB) ensureMessageReceiptsTable(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS message_receipts (
			message_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			participant_jid TEXT NOT NULL,
			delivered_ts INTEGER NOT NULL DEFAULT 0,
			read_ts INTEGER NOT NULL DEFAULT 0,
			played_ts INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (message_id, participant_jid),
			FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_message_receipts_chat ON message_receipts(chat_id)`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// UpsertMessageReceipt records one receipt for one participant. Stronger kinds
// imply weaker ones (played ⇒ read ⇒ delivered), and an already-set timestamp
// is never overwritten, so the stored time is always the earliest observed.
// The message row must exist (FK); callers fetch the message first anyway.
func (db *DB) UpsertMessageReceipt(ctx context.Context, messageID, chatID, participantJID, kind string, ts time.Time) error {
	if messageID == "" || participantJID == "" {
		return nil
	}
	defer db.timeOp("UpsertMessageReceipt", time.Now())
	tsUnix := ts.Unix()
	if ts.IsZero() || tsUnix <= 0 {
		tsUnix = time.Now().Unix()
	}

	var deliveredTs, readTs, playedTs int64
	switch kind {
	case ReceiptKindDelivered:
		deliveredTs = tsUnix
	case ReceiptKindRead:
		deliveredTs, readTs = tsUnix, tsUnix
	case ReceiptKindPlayed:
		deliveredTs, readTs, playedTs = tsUnix, tsUnix, tsUnix
	default:
		return nil
	}

	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO message_receipts (message_id, chat_id, participant_jid, delivered_ts, read_ts, played_ts)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, participant_jid) DO UPDATE SET
			delivered_ts = CASE WHEN message_receipts.delivered_ts = 0 THEN excluded.delivered_ts ELSE message_receipts.delivered_ts END,
			read_ts = CASE WHEN message_receipts.read_ts = 0 AND excluded.read_ts != 0 THEN excluded.read_ts ELSE message_receipts.read_ts END,
			played_ts = CASE WHEN message_receipts.played_ts = 0 AND excluded.played_ts != 0 THEN excluded.played_ts ELSE message_receipts.played_ts END
	`, messageID, chatID, participantJID, deliveredTs, readTs, playedTs)
	return err
}

func (db *DB) ListMessageReceipts(ctx context.Context, messageID string) ([]MessageReceipt, error) {
	rows, err := db.reader().QueryContext(ctx, `
		SELECT message_id, chat_id, participant_jid, delivered_ts, read_ts, played_ts
		FROM message_receipts
		WHERE message_id = ?
		ORDER BY participant_jid ASC
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	receipts := make([]MessageReceipt, 0, 8)
	for rows.Next() {
		var receipt MessageReceipt
		if err := rows.Scan(
			&receipt.MessageID,
			&receipt.ChatID,
			&receipt.ParticipantJID,
			&receipt.DeliveredTs,
			&receipt.ReadTs,
			&receipt.PlayedTs,
		); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}
