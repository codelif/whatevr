package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Sticker is one sticker in the user's library, keyed by content hash. The
// same row serves every source (recents, favorites, store packs, chat cache):
// membership is expressed by last_used, is_favorite and pack_id.
type Sticker struct {
	// CacheKey is hex(FileSHA256). Rows created from app-state favorites lack
	// the plaintext hash and carry a provisional "enc:"+hex(FileEncSHA256)
	// key until the file is downloaded once and rekeyed.
	CacheKey          string
	EncCacheKey       string
	MimeType          string
	IsAnimated        bool
	Width             int32
	Height            int32
	LocalPath         string
	ArchivePath       string
	StickerPayload    []byte
	UploadPayload     []byte
	UploadTS          int64
	Emojis            string
	AccessibilityText string
	PackID            string
	PackOrder         int32
	IsFavorite        bool
	FavoriteTS        int64
	RecentWeight      float64
	LastUsed          int64
}

type StickerPack struct {
	ID                string
	Name              string
	Publisher         string
	Description       string
	Animated          bool
	Lottie            bool
	TrayImageID       string
	TrayLocalPath     string
	ImageDataHash     string
	StickerCount      int32
	StoreOrder        int32
	Installed         bool
	InstalledTS       int64
	ContentsFetchedAt int64
}

// IsProvisionalStickerKey reports whether key is an "enc:" placeholder that
// still needs rekeying to the plaintext content hash after first download.
func IsProvisionalStickerKey(key string) bool {
	return strings.HasPrefix(key, "enc:")
}

func (db *DB) ensureStickerTables(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS stickers (
			cache_key TEXT PRIMARY KEY,
			enc_cache_key TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT 'image/webp',
			is_animated INTEGER NOT NULL DEFAULT 0,
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			local_path TEXT NOT NULL DEFAULT '',
			archive_path TEXT NOT NULL DEFAULT '',
			sticker_payload BLOB NOT NULL DEFAULT x'',
			upload_payload BLOB NOT NULL DEFAULT x'',
			upload_ts INTEGER NOT NULL DEFAULT 0,
			emojis TEXT NOT NULL DEFAULT '',
			accessibility_text TEXT NOT NULL DEFAULT '',
			pack_id TEXT NOT NULL DEFAULT '',
			pack_order INTEGER NOT NULL DEFAULT 0,
			is_favorite INTEGER NOT NULL DEFAULT 0,
			favorite_ts INTEGER NOT NULL DEFAULT 0,
			recent_weight REAL NOT NULL DEFAULT 0,
			last_used INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stickers_pack ON stickers(pack_id, pack_order) WHERE pack_id != ''`,
		`CREATE INDEX IF NOT EXISTS idx_stickers_recent ON stickers(last_used DESC) WHERE last_used > 0`,
		`CREATE INDEX IF NOT EXISTS idx_stickers_favorite ON stickers(favorite_ts DESC) WHERE is_favorite = 1`,
		`CREATE INDEX IF NOT EXISTS idx_stickers_enc_key ON stickers(enc_cache_key) WHERE enc_cache_key != ''`,
		`CREATE TABLE IF NOT EXISTS sticker_packs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			publisher TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			animated INTEGER NOT NULL DEFAULT 0,
			lottie INTEGER NOT NULL DEFAULT 0,
			tray_image_id TEXT NOT NULL DEFAULT '',
			tray_local_path TEXT NOT NULL DEFAULT '',
			image_data_hash TEXT NOT NULL DEFAULT '',
			sticker_count INTEGER NOT NULL DEFAULT 0,
			store_order INTEGER NOT NULL DEFAULT 0,
			installed INTEGER NOT NULL DEFAULT 0,
			installed_ts INTEGER NOT NULL DEFAULT 0,
			contents_fetched_at INTEGER NOT NULL DEFAULT 0
		)`,
	} {
		if _, err := db.conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

const stickerColumns = `cache_key, enc_cache_key, mime_type, is_animated, width, height,
	local_path, archive_path, sticker_payload, upload_payload, upload_ts,
	emojis, accessibility_text, pack_id, pack_order,
	is_favorite, favorite_ts, recent_weight, last_used`

func scanSticker(scanner interface{ Scan(...any) error }) (Sticker, error) {
	var s Sticker
	err := scanner.Scan(&s.CacheKey, &s.EncCacheKey, &s.MimeType, &s.IsAnimated, &s.Width, &s.Height,
		&s.LocalPath, &s.ArchivePath, &s.StickerPayload, &s.UploadPayload, &s.UploadTS,
		&s.Emojis, &s.AccessibilityText, &s.PackID, &s.PackOrder,
		&s.IsFavorite, &s.FavoriteTS, &s.RecentWeight, &s.LastUsed)
	return s, err
}

func (db *DB) GetSticker(ctx context.Context, cacheKey string) (Sticker, bool, error) {
	row := db.reader().QueryRowContext(ctx, `SELECT `+stickerColumns+` FROM stickers WHERE cache_key = ?`, cacheKey)
	s, err := scanSticker(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Sticker{}, false, nil
	}
	if err != nil {
		return Sticker{}, false, err
	}
	return s, true, nil
}

func (db *DB) GetStickerByEncKey(ctx context.Context, encKey string) (Sticker, bool, error) {
	row := db.reader().QueryRowContext(ctx, `
		SELECT `+stickerColumns+` FROM stickers WHERE enc_cache_key = ? LIMIT 1
	`, encKey)
	s, err := scanSticker(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Sticker{}, false, nil
	}
	if err != nil {
		return Sticker{}, false, err
	}
	return s, true, nil
}

// UpsertPackSticker writes a sticker discovered in a store pack. Pack
// metadata always wins (it is the authoritative source for emoji tags and
// ordering), while library state (files, favorites, recency, upload cache)
// is preserved on conflict.
func (db *DB) UpsertPackSticker(ctx context.Context, s Sticker) error {
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO stickers (cache_key, enc_cache_key, mime_type, is_animated, width, height,
			sticker_payload, emojis, accessibility_text, pack_id, pack_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			enc_cache_key = CASE WHEN excluded.enc_cache_key != '' THEN excluded.enc_cache_key ELSE stickers.enc_cache_key END,
			mime_type = excluded.mime_type,
			is_animated = excluded.is_animated,
			width = excluded.width,
			height = excluded.height,
			sticker_payload = CASE WHEN length(excluded.sticker_payload) > 0 THEN excluded.sticker_payload ELSE stickers.sticker_payload END,
			emojis = excluded.emojis,
			accessibility_text = excluded.accessibility_text,
			pack_id = excluded.pack_id,
			pack_order = excluded.pack_order,
			updated_at = unixepoch()
	`, s.CacheKey, s.EncCacheKey, s.MimeType, s.IsAnimated, s.Width, s.Height,
		nonNilBytes(s.StickerPayload), s.Emojis, s.AccessibilityText, s.PackID, s.PackOrder)
	return err
}

// TouchRecentSticker records a sticker in the recents source, creating the
// row if needed. last_used only moves forward; metadata fills empty fields
// without clobbering richer data from packs.
func (db *DB) TouchRecentSticker(ctx context.Context, s Sticker) error {
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO stickers (cache_key, enc_cache_key, mime_type, is_animated, width, height,
			sticker_payload, recent_weight, last_used)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			enc_cache_key = CASE WHEN excluded.enc_cache_key != '' THEN excluded.enc_cache_key ELSE stickers.enc_cache_key END,
			sticker_payload = CASE WHEN length(stickers.sticker_payload) = 0 THEN excluded.sticker_payload ELSE stickers.sticker_payload END,
			recent_weight = MAX(stickers.recent_weight, excluded.recent_weight),
			last_used = MAX(stickers.last_used, excluded.last_used),
			updated_at = unixepoch()
	`, s.CacheKey, s.EncCacheKey, s.MimeType, s.IsAnimated, s.Width, s.Height,
		nonNilBytes(s.StickerPayload), s.RecentWeight, s.LastUsed)
	return err
}

// SetStickerFavorite records a favorite/unfavorite from app state, creating
// the row if needed (favorites can reference stickers we have never seen).
func (db *DB) SetStickerFavorite(ctx context.Context, s Sticker, favorite bool, ts time.Time) error {
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO stickers (cache_key, enc_cache_key, mime_type, is_animated, width, height,
			sticker_payload, is_favorite, favorite_ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			enc_cache_key = CASE WHEN excluded.enc_cache_key != '' THEN excluded.enc_cache_key ELSE stickers.enc_cache_key END,
			sticker_payload = CASE WHEN length(stickers.sticker_payload) = 0 THEN excluded.sticker_payload ELSE stickers.sticker_payload END,
			is_favorite = excluded.is_favorite,
			favorite_ts = excluded.favorite_ts,
			updated_at = unixepoch()
	`, s.CacheKey, s.EncCacheKey, s.MimeType, s.IsAnimated, s.Width, s.Height,
		nonNilBytes(s.StickerPayload), favorite, ts.Unix())
	return err
}

// SetStickerFavoriteByEncKey applies a favorite flag to whichever row
// currently matches the encrypted-content key (handles mutations arriving
// after the row was rekeyed to its plaintext hash). Returns false when no
// row matched.
func (db *DB) SetStickerFavoriteByEncKey(ctx context.Context, encKey string, favorite bool, ts time.Time) (bool, error) {
	result, err := db.conn.ExecContext(ctx, `
		UPDATE stickers SET is_favorite = ?, favorite_ts = ?, updated_at = unixepoch()
		WHERE enc_cache_key = ? OR cache_key = ?
	`, favorite, ts.Unix(), encKey, "enc:"+encKey)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// ReconcileStickerFavorites replaces the favorites set from a full app-state
// snapshot. Each entry must carry EncCacheKey (favorite mutations have no
// plaintext hash) and FavoriteTS; rows are matched by encrypted-content key
// so rekeyed canonical rows keep their favorite flag.
func (db *DB) ReconcileStickerFavorites(ctx context.Context, favorites []Sticker) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE stickers SET is_favorite = 0 WHERE is_favorite = 1`); err != nil {
		return err
	}
	for _, s := range favorites {
		if s.EncCacheKey == "" {
			continue
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE stickers SET is_favorite = 1, favorite_ts = ?, updated_at = unixepoch()
			WHERE enc_cache_key = ? OR cache_key = ?
		`, s.FavoriteTS, s.EncCacheKey, "enc:"+s.EncCacheKey)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stickers (cache_key, enc_cache_key, mime_type, is_animated, width, height,
				sticker_payload, is_favorite, favorite_ts)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT(cache_key) DO UPDATE SET
				is_favorite = 1,
				favorite_ts = excluded.favorite_ts,
				updated_at = unixepoch()
		`, "enc:"+s.EncCacheKey, s.EncCacheKey, s.MimeType, s.IsAnimated, s.Width, s.Height,
			nonNilBytes(s.StickerPayload), s.FavoriteTS); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetStickerFile records the on-disk paths and the animation flag discovered
// while downloading. is_animated is written here (not just at ingest) because
// WhatsApp's library metadata carries no animation flag for WebP — only the
// file bytes reveal it — so the download is the first point we know for sure.
func (db *DB) SetStickerFile(ctx context.Context, cacheKey, localPath, archivePath string, isAnimated bool) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE stickers SET local_path = ?, archive_path = ?, is_animated = ?, updated_at = unixepoch()
		WHERE cache_key = ?
	`, localPath, archivePath, isAnimated, cacheKey)
	return err
}

// SetStickerAnimated flips just the animation flag, used by the one-time
// backfill that re-inspects already-cached WebP files.
func (db *DB) SetStickerAnimated(ctx context.Context, cacheKey string, isAnimated bool) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE stickers SET is_animated = ?, updated_at = unixepoch()
		WHERE cache_key = ?
	`, isAnimated, cacheKey)
	return err
}

// ListDownloadedWebPStickersUnflagged returns cached WebP stickers still marked
// static, so the backfill can re-inspect their bytes for animation. Only the
// cache key and local path are needed; payloads are skipped to keep it cheap.
func (db *DB) ListDownloadedWebPStickersUnflagged(ctx context.Context) ([]Sticker, error) {
	rows, err := db.reader().QueryContext(ctx, `
		SELECT cache_key, local_path FROM stickers
		WHERE local_path != '' AND mime_type = 'image/webp' AND is_animated = 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []Sticker
	for rows.Next() {
		var s Sticker
		if err := rows.Scan(&s.CacheKey, &s.LocalPath); err != nil {
			return nil, err
		}
		stickers = append(stickers, s)
	}
	return stickers, rows.Err()
}

func (db *DB) SetStickerUploadPayload(ctx context.Context, cacheKey string, payload []byte, ts time.Time) error {
	uploadTS := ts.Unix()
	if len(payload) == 0 {
		uploadTS = 0
	}
	_, err := db.conn.ExecContext(ctx, `
		UPDATE stickers SET upload_payload = ?, upload_ts = ?, updated_at = unixepoch()
		WHERE cache_key = ?
	`, nonNilBytes(payload), uploadTS, cacheKey)
	return err
}

func (db *DB) MarkStickerUsed(ctx context.Context, cacheKey string, ts time.Time) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE stickers SET last_used = MAX(last_used, ?), updated_at = unixepoch()
		WHERE cache_key = ?
	`, ts.Unix(), cacheKey)
	return err
}

func (db *DB) ClearStickerRecency(ctx context.Context, encKey string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE stickers SET last_used = 0, recent_weight = 0, updated_at = unixepoch()
		WHERE enc_cache_key = ? OR cache_key = ? OR cache_key = ?
	`, encKey, encKey, "enc:"+encKey)
	return err
}

// RekeySticker moves a provisionally keyed row to its canonical plaintext
// content hash once the file has been downloaded. If a canonical row already
// exists the two are merged: source membership is OR-ed / max-ed, richer
// metadata wins, and the provisional row is removed.
func (db *DB) RekeySticker(ctx context.Context, oldKey, newKey string) (Sticker, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return Sticker{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `SELECT `+stickerColumns+` FROM stickers WHERE cache_key = ?`, oldKey)
	old, err := scanSticker(row)
	if err != nil {
		return Sticker{}, err
	}

	row = tx.QueryRowContext(ctx, `SELECT `+stickerColumns+` FROM stickers WHERE cache_key = ?`, newKey)
	_, err = scanSticker(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `UPDATE stickers SET cache_key = ?, updated_at = unixepoch() WHERE cache_key = ?`, newKey, oldKey); err != nil {
			return Sticker{}, err
		}
		old.CacheKey = newKey
		if err := tx.Commit(); err != nil {
			return Sticker{}, err
		}
		return old, nil
	case err != nil:
		return Sticker{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE stickers SET
			enc_cache_key = CASE WHEN enc_cache_key = '' THEN ? ELSE enc_cache_key END,
			local_path = CASE WHEN local_path = '' THEN ? ELSE local_path END,
			archive_path = CASE WHEN archive_path = '' THEN ? ELSE archive_path END,
			sticker_payload = CASE WHEN length(sticker_payload) = 0 THEN ? ELSE sticker_payload END,
			is_favorite = MAX(is_favorite, ?),
			favorite_ts = MAX(favorite_ts, ?),
			recent_weight = MAX(recent_weight, ?),
			last_used = MAX(last_used, ?),
			updated_at = unixepoch()
		WHERE cache_key = ?
	`, old.EncCacheKey, old.LocalPath, old.ArchivePath, old.StickerPayload,
		old.IsFavorite, old.FavoriteTS, old.RecentWeight, old.LastUsed, newKey); err != nil {
		return Sticker{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stickers WHERE cache_key = ?`, oldKey); err != nil {
		return Sticker{}, err
	}
	if err := tx.Commit(); err != nil {
		return Sticker{}, err
	}

	row = db.conn.QueryRowContext(ctx, `SELECT `+stickerColumns+` FROM stickers WHERE cache_key = ?`, newKey)
	return scanSticker(row)
}

func (db *DB) listStickers(ctx context.Context, query string, args ...any) ([]Sticker, error) {
	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stickers []Sticker
	for rows.Next() {
		s, err := scanSticker(rows)
		if err != nil {
			return nil, err
		}
		stickers = append(stickers, s)
	}
	return stickers, rows.Err()
}

func (db *DB) ListRecentStickers(ctx context.Context, limit int) ([]Sticker, error) {
	return db.listStickers(ctx, `
		SELECT `+stickerColumns+` FROM stickers
		WHERE last_used > 0
		ORDER BY last_used DESC, recent_weight DESC
		LIMIT ?
	`, limit)
}

func (db *DB) ListFavoriteStickers(ctx context.Context, limit int) ([]Sticker, error) {
	return db.listStickers(ctx, `
		SELECT `+stickerColumns+` FROM stickers
		WHERE is_favorite = 1
		ORDER BY favorite_ts DESC
		LIMIT ?
	`, limit)
}

func (db *DB) ListPackStickers(ctx context.Context, packID string) ([]Sticker, error) {
	return db.listStickers(ctx, `
		SELECT `+stickerColumns+` FROM stickers
		WHERE pack_id = ?
		ORDER BY pack_order ASC
	`, packID)
}

// ListAllStickers returns the whole library: anything downloaded or known
// from a source, most recently touched first.
func (db *DB) ListAllStickers(ctx context.Context, limit int) ([]Sticker, error) {
	return db.listStickers(ctx, `
		SELECT `+stickerColumns+` FROM stickers
		ORDER BY MAX(last_used, favorite_ts) DESC, updated_at DESC
		LIMIT ?
	`, limit)
}

// SearchStickers matches emoji tags, accessibility text and pack names.
func (db *DB) SearchStickers(ctx context.Context, query string, limit int) ([]Sticker, error) {
	pattern := "%" + escapeLike(strings.TrimSpace(query)) + "%"
	return db.listStickers(ctx, `
		SELECT `+stickerColumns+` FROM stickers
		WHERE emojis LIKE ? ESCAPE '\'
		   OR accessibility_text LIKE ? ESCAPE '\' COLLATE NOCASE
		   OR (pack_id != '' AND pack_id IN (
		         SELECT id FROM sticker_packs WHERE name LIKE ? ESCAPE '\' COLLATE NOCASE))
		ORDER BY MAX(last_used, favorite_ts) DESC, pack_id ASC, pack_order ASC
		LIMIT ?
	`, pattern, pattern, pattern, limit)
}

func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

// UpsertStickerPacks refreshes the store index. Local state (installed flag,
// downloaded tray art) survives; a changed image_data_hash resets
// contents_fetched_at so the pack body is refetched on next open.
func (db *DB) UpsertStickerPacks(ctx context.Context, packs []StickerPack) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range packs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sticker_packs (id, name, publisher, description, animated, lottie,
				tray_image_id, image_data_hash, sticker_count, store_order)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				name = excluded.name,
				publisher = excluded.publisher,
				description = excluded.description,
				animated = excluded.animated,
				lottie = excluded.lottie,
				tray_local_path = CASE WHEN excluded.tray_image_id != sticker_packs.tray_image_id THEN '' ELSE sticker_packs.tray_local_path END,
				tray_image_id = excluded.tray_image_id,
				contents_fetched_at = CASE WHEN excluded.image_data_hash != sticker_packs.image_data_hash THEN 0 ELSE sticker_packs.contents_fetched_at END,
				image_data_hash = excluded.image_data_hash,
				sticker_count = CASE WHEN excluded.sticker_count > 0 THEN excluded.sticker_count ELSE sticker_packs.sticker_count END,
				store_order = excluded.store_order
		`, p.ID, p.Name, p.Publisher, p.Description, p.Animated, p.Lottie,
			p.TrayImageID, p.ImageDataHash, p.StickerCount, p.StoreOrder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const stickerPackColumns = `id, name, publisher, description, animated, lottie,
	tray_image_id, tray_local_path, image_data_hash, sticker_count, store_order,
	installed, installed_ts, contents_fetched_at`

func scanStickerPack(scanner interface{ Scan(...any) error }) (StickerPack, error) {
	var p StickerPack
	err := scanner.Scan(&p.ID, &p.Name, &p.Publisher, &p.Description, &p.Animated, &p.Lottie,
		&p.TrayImageID, &p.TrayLocalPath, &p.ImageDataHash, &p.StickerCount, &p.StoreOrder,
		&p.Installed, &p.InstalledTS, &p.ContentsFetchedAt)
	return p, err
}

// ListStickerPacks returns installed packs first (most recently installed
// leading), then the rest in store order.
func (db *DB) ListStickerPacks(ctx context.Context) ([]StickerPack, error) {
	rows, err := db.reader().QueryContext(ctx, `
		SELECT `+stickerPackColumns+` FROM sticker_packs
		ORDER BY installed DESC, CASE WHEN installed = 1 THEN -installed_ts ELSE store_order END ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var packs []StickerPack
	for rows.Next() {
		p, err := scanStickerPack(rows)
		if err != nil {
			return nil, err
		}
		packs = append(packs, p)
	}
	return packs, rows.Err()
}

func (db *DB) GetStickerPack(ctx context.Context, id string) (StickerPack, bool, error) {
	row := db.reader().QueryRowContext(ctx, `SELECT `+stickerPackColumns+` FROM sticker_packs WHERE id = ?`, id)
	p, err := scanStickerPack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StickerPack{}, false, nil
	}
	if err != nil {
		return StickerPack{}, false, err
	}
	return p, true, nil
}

func (db *DB) SetStickerPackInstalled(ctx context.Context, id string, installed bool, ts time.Time) error {
	installedTS := ts.Unix()
	if !installed {
		installedTS = 0
	}
	_, err := db.conn.ExecContext(ctx, `
		UPDATE sticker_packs SET installed = ?, installed_ts = ? WHERE id = ?
	`, installed, installedTS, id)
	return err
}

func (db *DB) SetStickerPackTrayPath(ctx context.Context, id, path string) error {
	_, err := db.conn.ExecContext(ctx, `UPDATE sticker_packs SET tray_local_path = ? WHERE id = ?`, path, id)
	return err
}

// MarkStickerPackContentsFetched stamps a successful FetchStickerPack and
// records the authoritative sticker count.
func (db *DB) MarkStickerPackContentsFetched(ctx context.Context, id string, count int32, ts time.Time) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE sticker_packs SET contents_fetched_at = ?, sticker_count = ? WHERE id = ?
	`, ts.Unix(), count, id)
	return err
}

// ClearPackStickerMembership detaches all stickers from a pack before its
// contents are re-imported, so stickers dropped from the pack do not linger
// under it. Rows keep their favorite/recent membership.
func (db *DB) ClearPackStickerMembership(ctx context.Context, packID string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE stickers SET pack_id = '', pack_order = 0 WHERE pack_id = ?
	`, packID)
	return err
}

func (db *DB) GetAppStateValue(ctx context.Context, key string) (string, error) {
	var value string
	err := db.reader().QueryRowContext(ctx, `SELECT value FROM app_state WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (db *DB) SetAppStateValue(ctx context.Context, key, value string) error {
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO app_state (key, value, updated_at) VALUES (?, ?, unixepoch())
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch()
	`, key, value)
	return err
}
