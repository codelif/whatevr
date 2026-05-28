package store

import (
	"context"
	"database/sql"
	"time"
)

const (
	HistorySyncStatusPending    = "pending"
	HistorySyncStatusProcessing = "processing"
	HistorySyncStatusProcessed  = "processed"
	HistorySyncStatusAcked      = "acked"
	HistorySyncStatusFailed     = "failed"
)

type HistorySyncChunk struct {
	ID            string
	SyncType      int32
	ChunkOrder    uint32
	Progress      uint32
	FileLength    uint64
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	EncHandle     string
	InlinePayload []byte
	Status        string
	Attempts      int32
	LastError     string
}

func (db *DB) SaveHistorySyncChunk(ctx context.Context, chunk HistorySyncChunk) (bool, error) {
	if chunk.ID == "" {
		return false, nil
	}
	inserted := true
	if err := db.conn.QueryRowContext(ctx, `SELECT 1 FROM history_sync_chunks WHERE id = ?`, chunk.ID).Scan(new(int)); err == nil {
		inserted = false
	} else if err != sql.ErrNoRows {
		return false, err
	}
	chunk.MediaKey = nonNilBytes(chunk.MediaKey)
	chunk.FileSHA256 = nonNilBytes(chunk.FileSHA256)
	chunk.FileEncSHA256 = nonNilBytes(chunk.FileEncSHA256)
	chunk.InlinePayload = nonNilBytes(chunk.InlinePayload)
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO history_sync_chunks (id, sync_type, chunk_order, progress, file_length, direct_path, media_key, file_sha256, file_enc_sha256, enc_handle, inline_payload, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			sync_type = excluded.sync_type,
			chunk_order = excluded.chunk_order,
			progress = excluded.progress,
			file_length = CASE WHEN excluded.file_length > 0 THEN excluded.file_length ELSE history_sync_chunks.file_length END,
			direct_path = CASE WHEN excluded.direct_path != '' THEN excluded.direct_path ELSE history_sync_chunks.direct_path END,
			media_key = CASE WHEN length(excluded.media_key) > 0 THEN excluded.media_key ELSE history_sync_chunks.media_key END,
			file_sha256 = CASE WHEN length(excluded.file_sha256) > 0 THEN excluded.file_sha256 ELSE history_sync_chunks.file_sha256 END,
			file_enc_sha256 = CASE WHEN length(excluded.file_enc_sha256) > 0 THEN excluded.file_enc_sha256 ELSE history_sync_chunks.file_enc_sha256 END,
			enc_handle = CASE WHEN excluded.enc_handle != '' THEN excluded.enc_handle ELSE history_sync_chunks.enc_handle END,
			inline_payload = CASE WHEN length(excluded.inline_payload) > 0 THEN excluded.inline_payload ELSE history_sync_chunks.inline_payload END,
			status = CASE WHEN history_sync_chunks.status = ? THEN history_sync_chunks.status ELSE ? END,
			last_error = CASE WHEN history_sync_chunks.status = ? THEN history_sync_chunks.last_error ELSE '' END,
			updated_at = unixepoch()
	`, chunk.ID, chunk.SyncType, chunk.ChunkOrder, chunk.Progress, int64(chunk.FileLength), chunk.DirectPath, chunk.MediaKey, chunk.FileSHA256, chunk.FileEncSHA256, chunk.EncHandle, chunk.InlinePayload, HistorySyncStatusPending, HistorySyncStatusAcked, HistorySyncStatusPending, HistorySyncStatusAcked)
	if err != nil {
		return false, err
	}
	return inserted, nil
}

func nonNilBytes(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

func (db *DB) ListRecoverableHistorySyncChunks(ctx context.Context, limit int) ([]HistorySyncChunk, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, sync_type, chunk_order, progress, file_length, direct_path, media_key, file_sha256, file_enc_sha256, enc_handle, inline_payload, status, attempts, last_error
		FROM history_sync_chunks
		WHERE status IN (?, ?, ?, ?)
		ORDER BY sync_type ASC, chunk_order ASC, created_at ASC
		LIMIT ?
	`, HistorySyncStatusPending, HistorySyncStatusProcessing, HistorySyncStatusProcessed, HistorySyncStatusFailed, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chunks := make([]HistorySyncChunk, 0, limit)
	for rows.Next() {
		chunk, err := scanHistorySyncChunk(rows)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return chunks, nil
}

func (db *DB) HasActiveHistorySyncChunks(ctx context.Context) (bool, error) {
	var count int
	err := db.conn.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM history_sync_chunks
		WHERE status IN (?, ?, ?, ?)
	`, HistorySyncStatusPending, HistorySyncStatusProcessing, HistorySyncStatusProcessed, HistorySyncStatusFailed).Scan(&count)
	return count > 0, err
}

func (db *DB) MarkHistorySyncChunkProcessing(ctx context.Context, id string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE history_sync_chunks
		SET status = ?, attempts = attempts + 1, last_error = '', updated_at = ?
		WHERE id = ? AND status != ?
	`, HistorySyncStatusProcessing, time.Now().Unix(), id, HistorySyncStatusAcked)
	return err
}

func (db *DB) MarkHistorySyncChunkProcessed(ctx context.Context, id string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE history_sync_chunks
		SET status = ?, last_error = '', updated_at = ?
		WHERE id = ?
	`, HistorySyncStatusProcessed, time.Now().Unix(), id)
	return err
}

func (db *DB) MarkHistorySyncChunkAcked(ctx context.Context, id string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE history_sync_chunks
		SET status = ?, last_error = '', updated_at = ?
		WHERE id = ?
	`, HistorySyncStatusAcked, time.Now().Unix(), id)
	return err
}

func (db *DB) MarkHistorySyncChunkFailed(ctx context.Context, id, errorText string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE history_sync_chunks
		SET status = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND status != ?
	`, HistorySyncStatusFailed, errorText, time.Now().Unix(), id, HistorySyncStatusAcked)
	return err
}

type historySyncChunkScanner interface {
	Scan(dest ...any) error
}

func scanHistorySyncChunk(row historySyncChunkScanner) (HistorySyncChunk, error) {
	var chunk HistorySyncChunk
	var chunkOrder int64
	var progress int64
	var fileLength int64
	var attempts int64
	err := row.Scan(
		&chunk.ID,
		&chunk.SyncType,
		&chunkOrder,
		&progress,
		&fileLength,
		&chunk.DirectPath,
		&chunk.MediaKey,
		&chunk.FileSHA256,
		&chunk.FileEncSHA256,
		&chunk.EncHandle,
		&chunk.InlinePayload,
		&chunk.Status,
		&attempts,
		&chunk.LastError,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return HistorySyncChunk{}, err
		}
		return HistorySyncChunk{}, err
	}
	chunk.ChunkOrder = uint32(chunkOrder)
	chunk.Progress = uint32(progress)
	chunk.FileLength = uint64(fileLength)
	chunk.Attempts = int32(attempts)
	return chunk, nil
}
