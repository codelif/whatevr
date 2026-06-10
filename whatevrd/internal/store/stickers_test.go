package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openStickerTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, ctx
}

// TestReadsDoNotBlockBehindWriter guards the reader-pool split: a read must
// proceed while the single writer connection is held open by an uncommitted
// transaction. With the old single-connection store this read would queue
// head-of-line behind the writer and time out — the exact stall that left the
// sticker picker spinning forever while history sync was writing.
func TestReadsDoNotBlockBehindWriter(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	if err := db.UpsertPackSticker(ctx, Sticker{
		CacheKey: "aabb", MimeType: "image/webp", Width: 512, Height: 512,
		StickerPayload: []byte("pack-payload"), PackID: "Cuppy", PackOrder: 1,
	}); err != nil {
		t.Fatalf("upsert pack sticker: %v", err)
	}

	// Hold the lone writer connection hostage in an open transaction.
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin writer tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE stickers SET pack_order = 2 WHERE cache_key = ?`, "aabb"); err != nil {
		t.Fatalf("writer exec: %v", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := db.GetSticker(readCtx, "aabb")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read blocked behind writer: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetSticker did not return while a writer tx was open")
	}
}

func TestStickerSourcesShareOneRow(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	if err := db.UpsertPackSticker(ctx, Sticker{
		CacheKey: "aabb", MimeType: "image/webp", Width: 512, Height: 512,
		StickerPayload: []byte("pack-payload"), Emojis: "😀 😎",
		PackID: "Cuppy", PackOrder: 3,
	}); err != nil {
		t.Fatalf("upsert pack sticker: %v", err)
	}
	if err := db.TouchRecentSticker(ctx, Sticker{CacheKey: "aabb", RecentWeight: 2.5, LastUsed: 1000}); err != nil {
		t.Fatalf("touch recent: %v", err)
	}
	if err := db.SetStickerFavorite(ctx, Sticker{CacheKey: "aabb"}, true, time.Unix(2000, 0)); err != nil {
		t.Fatalf("set favorite: %v", err)
	}

	s, ok, err := db.GetSticker(ctx, "aabb")
	if err != nil || !ok {
		t.Fatalf("get sticker: ok=%v err=%v", ok, err)
	}
	if s.PackID != "Cuppy" || s.LastUsed != 1000 || !s.IsFavorite || string(s.StickerPayload) != "pack-payload" {
		t.Fatalf("merged row wrong: %+v", s)
	}

	recents, err := db.ListRecentStickers(ctx, 10)
	if err != nil || len(recents) != 1 {
		t.Fatalf("recents: %v %d", err, len(recents))
	}
	favorites, err := db.ListFavoriteStickers(ctx, 10)
	if err != nil || len(favorites) != 1 {
		t.Fatalf("favorites: %v %d", err, len(favorites))
	}
	packStickers, err := db.ListPackStickers(ctx, "Cuppy")
	if err != nil || len(packStickers) != 1 {
		t.Fatalf("pack stickers: %v %d", err, len(packStickers))
	}
}

func TestTouchRecentStickerOnlyMovesForward(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	if err := db.TouchRecentSticker(ctx, Sticker{CacheKey: "k1", LastUsed: 500, RecentWeight: 3}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := db.TouchRecentSticker(ctx, Sticker{CacheKey: "k1", LastUsed: 100, RecentWeight: 1}); err != nil {
		t.Fatalf("touch older: %v", err)
	}
	s, _, err := db.GetSticker(ctx, "k1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if s.LastUsed != 500 || s.RecentWeight != 3 {
		t.Fatalf("recency regressed: %+v", s)
	}
}

func TestRekeyStickerMergesProvisionalRow(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	// Favorited via app state before the file ever existed locally.
	if err := db.SetStickerFavorite(ctx, Sticker{
		CacheKey: "enc:ff00", EncCacheKey: "ff00", StickerPayload: []byte("fav-payload"),
	}, true, time.Unix(300, 0)); err != nil {
		t.Fatalf("favorite provisional: %v", err)
	}
	// Same sticker also known canonically from a pack.
	if err := db.UpsertPackSticker(ctx, Sticker{
		CacheKey: "cafe", MimeType: "image/webp", Emojis: "🔥",
		StickerPayload: []byte("pack-payload"), PackID: "Hot", PackOrder: 1,
	}); err != nil {
		t.Fatalf("upsert pack: %v", err)
	}

	merged, err := db.RekeySticker(ctx, "enc:ff00", "cafe")
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if !merged.IsFavorite || merged.PackID != "Hot" || merged.EncCacheKey != "ff00" {
		t.Fatalf("merge lost state: %+v", merged)
	}
	if _, ok, _ := db.GetSticker(ctx, "enc:ff00"); ok {
		t.Fatal("provisional row should be deleted")
	}

	// Late mutation addressed by enc key still lands on the merged row.
	matched, err := db.SetStickerFavoriteByEncKey(ctx, "ff00", false, time.Unix(400, 0))
	if err != nil || !matched {
		t.Fatalf("favorite by enc key: matched=%v err=%v", matched, err)
	}
	s, _, _ := db.GetSticker(ctx, "cafe")
	if s.IsFavorite {
		t.Fatalf("unfavorite not applied: %+v", s)
	}
}

func TestRekeyStickerSimpleRename(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	if err := db.SetStickerFavorite(ctx, Sticker{CacheKey: "enc:aa", EncCacheKey: "aa"}, true, time.Unix(1, 0)); err != nil {
		t.Fatalf("favorite: %v", err)
	}
	s, err := db.RekeySticker(ctx, "enc:aa", "beef")
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if s.CacheKey != "beef" || !s.IsFavorite {
		t.Fatalf("rename wrong: %+v", s)
	}
}

func TestSearchStickers(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	if err := db.UpsertStickerPacks(ctx, []StickerPack{
		{ID: "Cuppy", Name: "Cuppy Love", ImageDataHash: "h1"},
	}); err != nil {
		t.Fatalf("upsert packs: %v", err)
	}
	if err := db.UpsertPackSticker(ctx, Sticker{CacheKey: "s1", Emojis: "😀 ☕", AccessibilityText: "happy cup", PackID: "Cuppy", PackOrder: 0}); err != nil {
		t.Fatalf("upsert s1: %v", err)
	}
	if err := db.UpsertPackSticker(ctx, Sticker{CacheKey: "s2", Emojis: "😭", AccessibilityText: "sad face", PackID: "Cuppy", PackOrder: 1}); err != nil {
		t.Fatalf("upsert s2: %v", err)
	}

	byEmoji, err := db.SearchStickers(ctx, "😀", 10)
	if err != nil || len(byEmoji) != 1 || byEmoji[0].CacheKey != "s1" {
		t.Fatalf("emoji search: %v %+v", err, byEmoji)
	}
	byText, err := db.SearchStickers(ctx, "sad", 10)
	if err != nil || len(byText) != 1 || byText[0].CacheKey != "s2" {
		t.Fatalf("text search: %v %+v", err, byText)
	}
	byPack, err := db.SearchStickers(ctx, "cuppy", 10)
	if err != nil || len(byPack) != 2 {
		t.Fatalf("pack-name search: %v %d", err, len(byPack))
	}
	escaped, err := db.SearchStickers(ctx, "100%", 10)
	if err != nil || len(escaped) != 0 {
		t.Fatalf("escaped search should match nothing: %v %d", err, len(escaped))
	}
}

func TestUpsertStickerPacksPreservesLocalState(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	if err := db.UpsertStickerPacks(ctx, []StickerPack{
		{ID: "p1", Name: "Pack One", TrayImageID: "tray1", ImageDataHash: "hash1", StoreOrder: 0},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.SetStickerPackInstalled(ctx, "p1", true, time.Unix(100, 0)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := db.SetStickerPackTrayPath(ctx, "p1", "/tmp/tray1.png"); err != nil {
		t.Fatalf("tray: %v", err)
	}
	if err := db.MarkStickerPackContentsFetched(ctx, "p1", 14, time.Unix(200, 0)); err != nil {
		t.Fatalf("mark fetched: %v", err)
	}

	// Same hash: everything local survives.
	if err := db.UpsertStickerPacks(ctx, []StickerPack{
		{ID: "p1", Name: "Pack One Renamed", TrayImageID: "tray1", ImageDataHash: "hash1", StoreOrder: 5},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	p, ok, err := db.GetStickerPack(ctx, "p1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if !p.Installed || p.TrayLocalPath != "/tmp/tray1.png" || p.ContentsFetchedAt != 200 || p.Name != "Pack One Renamed" {
		t.Fatalf("local state lost: %+v", p)
	}

	// Changed content hash: contents must be refetched; install state stays.
	if err := db.UpsertStickerPacks(ctx, []StickerPack{
		{ID: "p1", Name: "Pack One Renamed", TrayImageID: "tray2", ImageDataHash: "hash2", StoreOrder: 5},
	}); err != nil {
		t.Fatalf("hash-change upsert: %v", err)
	}
	p, _, err = db.GetStickerPack(ctx, "p1")
	if err != nil {
		t.Fatalf("get after hash change: %v", err)
	}
	if p.ContentsFetchedAt != 0 || p.TrayLocalPath != "" || !p.Installed {
		t.Fatalf("hash change handling wrong: %+v", p)
	}
}

func TestUploadPayloadRoundTrip(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	if err := db.TouchRecentSticker(ctx, Sticker{CacheKey: "u1", LastUsed: 1}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := db.SetStickerUploadPayload(ctx, "u1", []byte("uploaded"), time.Unix(500, 0)); err != nil {
		t.Fatalf("set upload: %v", err)
	}
	s, _, err := db.GetSticker(ctx, "u1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(s.UploadPayload) != "uploaded" || s.UploadTS != 500 {
		t.Fatalf("upload payload wrong: %+v", s)
	}
	if err := db.SetStickerUploadPayload(ctx, "u1", nil, time.Unix(600, 0)); err != nil {
		t.Fatalf("clear upload: %v", err)
	}
	s, _, _ = db.GetSticker(ctx, "u1")
	if len(s.UploadPayload) != 0 || s.UploadTS != 0 {
		t.Fatalf("upload payload not cleared: %+v", s)
	}
}

func TestAppStateValueRoundTrip(t *testing.T) {
	db, ctx := openStickerTestDB(t)

	value, err := db.GetAppStateValue(ctx, "missing")
	if err != nil || value != "" {
		t.Fatalf("missing key: %q %v", value, err)
	}
	if err := db.SetAppStateValue(ctx, "k", "v1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := db.SetAppStateValue(ctx, "k", "v2"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	value, err = db.GetAppStateValue(ctx, "k")
	if err != nil || value != "v2" {
		t.Fatalf("get: %q %v", value, err)
	}
}
