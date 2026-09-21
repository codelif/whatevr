package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

// StatusUpdate is one contact's status (story): text or a media payload.
// Statuses arrive from status@broadcast and live outside chats on purpose —
// storing them as chat messages would materialize a bogus "status" chat row.
type StatusUpdate struct {
	ID            string
	SenderID      string
	SenderName    string
	TimestampUnix int64
	Kind          string
	Text          string
	// TextBG is the text-status background color as ARGB (0 = default), and
	// TextFont is the WhatsApp font id (0 = system default).
	TextBG                  uint32
	TextFont                int32
	MediaMimeType           string
	MediaKind               string
	MediaLocalPath          string
	MediaThumbnailLocalPath string
	MediaWidth              int32
	MediaHeight             int32
	MediaPayload            []byte
	MediaDurationSecs       int32
	MediaSizeBytes          int64
	MediaFileName           string
	Viewed                  bool
}

// StatusUpdateInput is the ingest form of a StatusUpdate.
type StatusUpdateInput struct {
	ID                      string
	SenderID                string
	SenderName              string
	Timestamp               time.Time
	Kind                    string
	Text                    string
	TextBG                  uint32
	TextFont                int32
	MediaMimeType           string
	MediaKind               string
	MediaLocalPath          string
	MediaThumbnailLocalPath string
	MediaWidth              int32
	MediaHeight             int32
	MediaPayload            []byte
	MediaDurationSecs       int32
	MediaSizeBytes          int64
	MediaFileName           string
}

func (db *DB) SaveStatusUpdate(ctx context.Context, input StatusUpdateInput) (StatusUpdate, bool, error) {
	defer db.timeOp("SaveStatusUpdate", time.Now())
	if input.ID == "" {
		return StatusUpdate{}, false, errors.New("status id is required")
	}
	if input.SenderID == "" {
		return StatusUpdate{}, false, errors.New("status sender is required")
	}
	if input.Timestamp.IsZero() {
		input.Timestamp = time.Now()
	}
	if input.MediaPayload == nil {
		input.MediaPayload = []byte{}
	}

	result, err := db.conn.ExecContext(ctx, `
		INSERT INTO status_updates (id, sender_id, sender_name, timestamp, kind, text, text_bg, text_font, media_mime_type, media_kind, media_payload, media_duration_secs, media_size_bytes, media_file_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, input.ID, input.SenderID, input.SenderName, input.Timestamp.Unix(), input.Kind, input.Text, input.TextBG, input.TextFont, input.MediaMimeType, input.MediaKind, input.MediaPayload, input.MediaDurationSecs, input.MediaSizeBytes, input.MediaFileName)
	if err != nil {
		return StatusUpdate{}, false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return StatusUpdate{}, false, err
	}
	status, err := db.GetStatusUpdate(ctx, input.ID)
	if err != nil {
		return StatusUpdate{}, false, err
	}
	return status, rowsAffected > 0, nil
}

func statusUpdateFromRow(id, senderID, senderName string, timestampUnix int64, kind, text, mediaMimeType, mediaKind, mediaLocalPath, mediaThumbnailLocalPath string, mediaWidth, mediaHeight, mediaDurationSecs int32, mediaSizeBytes int64, mediaFileName string, mediaPayload []byte, viewed bool) StatusUpdate {
	return StatusUpdate{
		ID:                      id,
		SenderID:                senderID,
		SenderName:              senderName,
		TimestampUnix:           timestampUnix,
		Kind:                    kind,
		Text:                    text,
		MediaMimeType:           mediaMimeType,
		MediaKind:               mediaKind,
		MediaLocalPath:          mediaLocalPath,
		MediaThumbnailLocalPath: mediaThumbnailLocalPath,
		MediaWidth:              mediaWidth,
		MediaHeight:             mediaHeight,
		MediaPayload:            mediaPayload,
		MediaDurationSecs:       mediaDurationSecs,
		MediaSizeBytes:          mediaSizeBytes,
		MediaFileName:           mediaFileName,
		Viewed:                  viewed,
	}
}

func (db *DB) GetStatusUpdate(ctx context.Context, id string) (StatusUpdate, error) {
	defer db.timeOp("GetStatusUpdate", time.Now())
	var s StatusUpdate
	err := db.reader().QueryRowContext(ctx, `
		SELECT id, sender_id, sender_name, timestamp, kind, text, text_bg, text_font, media_mime_type, media_kind, media_local_path, media_thumbnail_local_path, media_width, media_height, media_payload, media_duration_secs, media_size_bytes, media_file_name, is_viewed
		FROM status_updates
		WHERE id = ?
	`, id).Scan(&s.ID, &s.SenderID, &s.SenderName, &s.TimestampUnix, &s.Kind, &s.Text, &s.TextBG, &s.TextFont, &s.MediaMimeType, &s.MediaKind, &s.MediaLocalPath, &s.MediaThumbnailLocalPath, &s.MediaWidth, &s.MediaHeight, &s.MediaPayload, &s.MediaDurationSecs, &s.MediaSizeBytes, &s.MediaFileName, &s.Viewed)
	if err != nil {
		return StatusUpdate{}, err
	}
	return s, nil
}

// ListStatusUpdates returns statuses newest first, optionally capped.
func (db *DB) ListStatusUpdates(ctx context.Context, limit int) ([]StatusUpdate, error) {
	defer db.timeOp("ListStatusUpdates", time.Now())
	query := `
		SELECT id, sender_id, sender_name, timestamp, kind, text, text_bg, text_font, media_mime_type, media_kind, media_local_path, media_thumbnail_local_path, media_width, media_height, media_payload, media_duration_secs, media_size_bytes, media_file_name, is_viewed
		FROM status_updates
		ORDER BY timestamp DESC, rowid DESC
	`
	args := []any{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	statuses := []StatusUpdate{}
	for rows.Next() {
		var s StatusUpdate
		if err := rows.Scan(&s.ID, &s.SenderID, &s.SenderName, &s.TimestampUnix, &s.Kind, &s.Text, &s.TextBG, &s.TextFont, &s.MediaMimeType, &s.MediaKind, &s.MediaLocalPath, &s.MediaThumbnailLocalPath, &s.MediaWidth, &s.MediaHeight, &s.MediaPayload, &s.MediaDurationSecs, &s.MediaSizeBytes, &s.MediaFileName, &s.Viewed); err != nil {
			return nil, err
		}
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return statuses, nil
}

// MarkStatusViewed flags a status as seen locally. Viewed receipts to the
// sender are a separate TODO; this only drives the local ring/badge state.
func (db *DB) MarkStatusViewed(ctx context.Context, id string) (StatusUpdate, error) {
	defer db.timeOp("MarkStatusViewed", time.Now())
	if _, err := db.conn.ExecContext(ctx, `UPDATE status_updates SET is_viewed = 1 WHERE id = ?`, id); err != nil {
		return StatusUpdate{}, err
	}
	return db.GetStatusUpdate(ctx, id)
}

// SetStatusMediaPath records a downloaded status payload's cache path plus
// its derived thumbnail path and dimensions (image media only). Text and audio
// statuses leave the thumbnail/dimension fields at their zero values.
func (db *DB) SetStatusMediaPath(ctx context.Context, id, localPath, thumbnailPath string, width, height int32) (StatusUpdate, error) {
	defer db.timeOp("SetStatusMediaPath", time.Now())
	if _, err := db.conn.ExecContext(ctx,
		`UPDATE status_updates SET media_local_path = ?, media_thumbnail_local_path = ?, media_width = ?, media_height = ? WHERE id = ?`,
		localPath, thumbnailPath, width, height, id); err != nil {
		return StatusUpdate{}, err
	}
	return db.GetStatusUpdate(ctx, id)
}

// DeleteStatusUpdate drops a status row (after a successful revoke, or for
// pruning a Tombstoned local post that never left).
func (db *DB) DeleteStatusUpdate(ctx context.Context, id string) error {
	defer db.timeOp("DeleteStatusUpdate", time.Now())
	_, err := db.conn.ExecContext(ctx, `DELETE FROM status_updates WHERE id = ?`, id)
	return err
}

// SetStatusKeepSender pins (or unpins) a contact's expired statuses: kept
// senders grow an archived section in the Status tab instead of having their
// older statuses hidden once past 24h.
func (db *DB) SetStatusKeepSender(ctx context.Context, senderID string, kept bool) error {
	defer db.timeOp("SetStatusKeepSender", time.Now())
	if kept {
		_, err := db.conn.ExecContext(ctx, `
			INSERT INTO status_keep_senders (sender_id, kept_at)
			VALUES (?, unixepoch())
			ON CONFLICT(sender_id) DO UPDATE SET kept_at = unixepoch()
		`, senderID)
		return err
	}
	_, err := db.conn.ExecContext(ctx, `DELETE FROM status_keep_senders WHERE sender_id = ?`, senderID)
	return err
}

// ListKeptStatusSenders returns the sender ids with status keep enabled,
// oldest-kept first.
func (db *DB) ListKeptStatusSenders(ctx context.Context) ([]string, error) {
	defer db.timeOp("ListKeptStatusSenders", time.Now())
	rows, err := db.reader().QueryContext(ctx, `
		SELECT sender_id FROM status_keep_senders ORDER BY kept_at ASC, sender_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kept := []string{}
	for rows.Next() {
		var senderID string
		if err := rows.Scan(&senderID); err != nil {
			return nil, err
		}
		kept = append(kept, senderID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return kept, nil
}

// SetStatusMutedSender hides (or unhides) a contact's statuses: muted
// senders collect under the Status tab's Muted section instead of the main
// list. Silent upsert — callers publish.
func (db *DB) SetStatusMutedSender(ctx context.Context, senderID string, muted bool) error {
	defer db.timeOp("SetStatusMutedSender", time.Now())
	if muted {
		_, err := db.conn.ExecContext(ctx, `
			INSERT INTO status_muted_senders (sender_id, muted_at)
			VALUES (?, unixepoch())
			ON CONFLICT(sender_id) DO UPDATE SET muted_at = unixepoch()
		`, senderID)
		return err
	}
	_, err := db.conn.ExecContext(ctx, `DELETE FROM status_muted_senders WHERE sender_id = ?`, senderID)
	return err
}

// ListMutedStatusSenders returns the sender ids with status mute enabled,
// oldest-muted first.
func (db *DB) ListMutedStatusSenders(ctx context.Context) ([]string, error) {
	defer db.timeOp("ListMutedStatusSenders", time.Now())
	rows, err := db.reader().QueryContext(ctx, `
		SELECT sender_id FROM status_muted_senders ORDER BY muted_at ASC, sender_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	muted := []string{}
	for rows.Next() {
		var senderID string
		if err := rows.Scan(&senderID); err != nil {
			return nil, err
		}
		muted = append(muted, senderID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return muted, nil
}

// ReplaceMutedStatusSenders reconciles the muted set to exactly ids (phone
// snapshot wins): unlisted senders are unmuted, missing ones muted. Empty ids
// clears the set.
func (db *DB) ReplaceMutedStatusSenders(ctx context.Context, ids []string) error {
	defer db.timeOp("ReplaceMutedStatusSenders", time.Now())
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM status_muted_senders`); err != nil {
		return err
	}
	// Sorted insert: muted_at ties break on insert order, so a fixed order
	// keeps ListMutedStatusSenders deterministic across syncs. Upsert, not
	// plain insert: a snapshot can repeat an id. Sorted on a copy — the
	// caller's slice order is not ours to change.
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for _, id := range sorted {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO status_muted_senders (sender_id, muted_at)
			VALUES (?, unixepoch())
			ON CONFLICT(sender_id) DO NOTHING
		`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneOldStatusUpdates drops statuses older than maxAge; WhatsApp statuses
// expire after 24h, so anything older is dead weight.
func (db *DB) PruneOldStatusUpdates(ctx context.Context, maxAge time.Duration) (int64, error) {
	defer db.timeOp("PruneOldStatusUpdates", time.Now())
	result, err := db.conn.ExecContext(ctx, `DELETE FROM status_updates WHERE timestamp < ?`, time.Now().Add(-maxAge).Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RecordStatusViewer records that viewerJID viewed a status (from its viewed
// receipt). Repeat views refresh the timestamp.
func (db *DB) RecordStatusViewer(ctx context.Context, statusID, viewerJID string, viewedAt time.Time) error {
	defer db.timeOp("RecordStatusViewer", time.Now())
	if statusID == "" || viewerJID == "" {
		return nil
	}
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO status_viewers (status_id, viewer_jid, viewed_at)
		VALUES (?, ?, ?)
		ON CONFLICT(status_id, viewer_jid) DO UPDATE SET viewed_at = excluded.viewed_at
	`, statusID, viewerJID, viewedAt.Unix())
	return err
}

// StatusViewer is one recorded view of our status.
type StatusViewer struct {
	ViewerJID string
	ViewedAt  int64
}

// ListStatusViewers returns who viewed a status, most recent first.
func (db *DB) ListStatusViewers(ctx context.Context, statusID string) ([]StatusViewer, error) {
	defer db.timeOp("ListStatusViewers", time.Now())
	rows, err := db.reader().QueryContext(ctx, `SELECT viewer_jid, viewed_at FROM status_viewers WHERE status_id = ? ORDER BY viewed_at DESC`, statusID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	viewers := []StatusViewer{}
	for rows.Next() {
		var viewer StatusViewer
		if err := rows.Scan(&viewer.ViewerJID, &viewer.ViewedAt); err != nil {
			return nil, err
		}
		viewers = append(viewers, viewer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return viewers, nil
}
