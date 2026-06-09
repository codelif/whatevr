package store

import (
	"context"
	"encoding/hex"

	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
)

// StickerCacheKeyFromPayload derives the content-addressed sticker cache key
// from a serialized waE2E.StickerMessage payload. An empty key means the
// payload carries no usable content hash.
func StickerCacheKeyFromPayload(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", nil
	}
	var sticker waE2E.StickerMessage
	if err := proto.Unmarshal(payload, &sticker); err != nil {
		return "", err
	}
	if hash := sticker.GetFileSHA256(); len(hash) > 0 {
		return hex.EncodeToString(hash), nil
	}
	if hash := sticker.GetFileEncSHA256(); len(hash) > 0 {
		return hex.EncodeToString(hash), nil
	}
	return "", nil
}

// ensureStickerCacheKeys backfills messages.media_cache_key for sticker rows
// written before the column was populated on insert. With keys in place the
// indexed DownloadedStickerPathByCacheKey lookup is authoritative and no
// full-table sticker scan is ever needed. The WHERE clause is a prefix of
// idx_messages_sticker_cache_key, so once the backlog is empty this probe is
// free on every subsequent startup.
func (db *DB) ensureStickerCacheKeys(ctx context.Context) error {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, media_payload
		FROM messages
		WHERE media_kind = ? AND media_cache_key = '' AND media_payload != x''
	`, MediaKindSticker)
	if err != nil {
		return err
	}
	defer rows.Close()

	type pendingKey struct {
		id  string
		key string
	}
	var pending []pendingKey
	for rows.Next() {
		var id string
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			return err
		}
		key, err := StickerCacheKeyFromPayload(payload)
		if err != nil || key == "" {
			// Undecodable or hash-less payloads simply keep an empty key;
			// they were never matchable by content before either.
			continue
		}
		pending = append(pending, pendingKey{id: id, key: key})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, entry := range pending {
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages SET media_cache_key = ? WHERE id = ?
		`, entry.key, entry.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
