package protocol

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

func startStickerProtocolServer(t *testing.T) (string, *app.Daemon, *store.DB, *Server) {
	t.Helper()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod socket dir: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")

	db, err := store.Open(context.Background(), filepath.Join(dir, "whatevrd.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	daemon := app.NewDaemon(app.Paths{DataDir: "/data-dir", CacheDir: "/cache-dir"})
	daemon.SetState(app.StateOnline)

	ctx, cancel := context.WithCancel(context.Background())
	server, err := Start(ctx, socketPath, daemon)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		for err := range server.Err() {
			t.Errorf("server error during shutdown: %v", err)
		}
	})
	RegisterDaemonViews(server, daemon, db, nil)

	return socketPath, daemon, db, server
}

func seedRecentSticker(t *testing.T, db *store.DB, key string, lastUsed int64) {
	t.Helper()
	if err := db.TouchRecentSticker(context.Background(), store.Sticker{
		CacheKey:     key,
		MimeType:     "image/webp",
		Width:        512,
		Height:       512,
		RecentWeight: float64(lastUsed),
		LastUsed:     lastUsed,
	}); err != nil {
		t.Fatalf("seed recent sticker %s: %v", key, err)
	}
}

func seedStickerPack(t *testing.T, db *store.DB, pack store.StickerPack) {
	t.Helper()
	if err := db.UpsertStickerPacks(context.Background(), []store.StickerPack{pack}); err != nil {
		t.Fatalf("seed sticker pack %s: %v", pack.ID, err)
	}
}

func TestStickersViewRecentWindowAndExtend(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	seedRecentSticker(t, db, "old", 100)
	seedRecentSticker(t, db, "new", 200)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"stickers","source":"recent","limit":1}`)

	newest := c.expectUpsert(sub, "new")
	item := newest["item"].(map[string]any)
	if item["cache_key"] != "new" || item["mime_type"] != "image/webp" || item["width"] != float64(512) {
		t.Fatalf("recent sticker item = %v", item)
	}
	c.expectReady(sub, false)

	c.extend(3, sub, 1, "older")
	c.expectUpsert(sub, "old")
	c.expectReady(sub, true)
}

func TestStickersViewFavoriteLiveUpdate(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	if err := db.SetStickerFavorite(context.Background(), store.Sticker{CacheKey: "fav-a", MimeType: "image/webp"}, true, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"stickers","source":"favorite"}`)
	c.expectUpsert(sub, "fav-a")
	c.expectReady(sub, true)

	if err := db.SetStickerFavorite(context.Background(), store.Sticker{CacheKey: "fav-b", MimeType: "image/webp"}, true, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	daemon.PublishStickerLibraryChanged(app.StickerSourceFavorite)
	c.expectUpsert(sub, "fav-b")
}

func TestFavoriteStickersViewInvalidatesOnRecencyChange(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	if err := db.SetStickerFavorite(context.Background(), store.Sticker{CacheKey: "fav-a", MimeType: "image/webp"}, true, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"stickers","source":"favorite"}`)
	c.expectUpsert(sub, "fav-a")
	c.expectReady(sub, true)

	if err := db.TouchRecentSticker(context.Background(), store.Sticker{CacheKey: "fav-a", MimeType: "image/webp", LastUsed: 200, RecentWeight: 3.5}); err != nil {
		t.Fatal(err)
	}
	daemon.PublishStickerLibraryChanged(app.StickerSourceRecent)
	updated := c.expectUpsert(sub, "fav-a")
	item := updated["item"].(map[string]any)
	if item["last_used_unix"] != float64(200) || item["weight"] != 3.5 {
		t.Fatalf("favorite row recency after update = %v", item)
	}
}

func TestStickersViewRequiresValidSource(t *testing.T) {
	socketPath, _, _ := startChatsTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"stickers"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("missing source error code = %q, want %q", code, CodeInvalidParams)
	}
}

func TestStickerPacksViewFillAndLiveUpdate(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	seedStickerPack(t, db, store.StickerPack{ID: "p1", Name: "One", Publisher: "Acme", StickerCount: 2, StoreOrder: 1})
	seedStickerPack(t, db, store.StickerPack{ID: "p2", Name: "Two", Publisher: "Acme", StickerCount: 3, StoreOrder: 2})
	if err := db.SetStickerPackInstalled(context.Background(), "p2", true, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"sticker_packs"}`)

	installed := c.expectUpsert(sub, "p2")
	item := installed["item"].(map[string]any)
	if item["name"] != "Two" || item["installed"] != true || item["sticker_count"] != float64(3) {
		t.Fatalf("installed pack item = %v", item)
	}
	c.expectUpsert(sub, "p1")
	c.expectReady(sub, true)

	if err := db.SetStickerPackTrayPath(context.Background(), "p1", "/cache/tray.png"); err != nil {
		t.Fatal(err)
	}
	daemon.PublishStickerLibraryChanged(app.StickerSourceUnspecified)
	updated := c.expectUpsert(sub, "p1")
	if got := updated["item"].(map[string]any)["tray_local_path"]; got != "/cache/tray.png" {
		t.Fatalf("tray path after update = %v", got)
	}
}

type fakeStickerActions struct {
	db      *store.DB
	release chan struct{}
	onFetch func(context.Context, string) error
	once    sync.Once
}

func (f *fakeStickerActions) ListStickerPacks(ctx context.Context, _ bool) ([]store.StickerPack, error) {
	return f.db.ListStickerPacks(ctx)
}

func (f *fakeStickerActions) GetStickerPack(ctx context.Context, packID string) (store.StickerPack, []store.Sticker, error) {
	<-f.release
	f.once.Do(func() {
		if f.onFetch != nil {
			_ = f.onFetch(ctx, packID)
		}
	})
	pack, _, err := f.db.GetStickerPack(ctx, packID)
	if err != nil {
		return store.StickerPack{}, nil, err
	}
	stickers, err := f.db.ListPackStickers(ctx, packID)
	return pack, stickers, err
}

func TestStickerPackViewAsyncFetchAndDownloadUpdate(t *testing.T) {
	socketPath, daemon, db, server := startStickerProtocolServer(t)
	seedStickerPack(t, db, store.StickerPack{ID: "pack-1", Name: "Pack One", StickerCount: 1})
	actions := &fakeStickerActions{
		db:      db,
		release: make(chan struct{}),
		onFetch: func(ctx context.Context, packID string) error {
			if err := db.UpsertPackSticker(ctx, store.Sticker{CacheKey: "s1", MimeType: "image/webp", Width: 256, Height: 256, PackID: packID, PackOrder: 7, Emojis: "hi wave"}); err != nil {
				return err
			}
			return db.MarkStickerPackContentsFetched(ctx, packID, 1, time.Unix(300, 0))
		},
	}
	// Replace the nil-actions registration from the helper with the
	// fake action seam so contents fetch after subscribe.
	server.RegisterView("sticker_pack", stickerPackView{daemon: daemon, store: db, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"sticker_pack","pack_id":"pack-1"}`)
	c.expectReady(sub, true) // pack shell exists; contents have not been fetched yet.

	close(actions.release)
	upsert := c.expectUpsert(sub, "s1")
	item := upsert["item"].(map[string]any)
	if item["pack_id"] != "pack-1" || item["width"] != float64(256) {
		t.Fatalf("fetched pack sticker item = %v", item)
	}

	if err := db.SetStickerFile(context.Background(), "s1", "/cache/stickers/s1.webp", "", false); err != nil {
		t.Fatal(err)
	}
	daemon.PublishStickerDownloadChanged(app.Sticker{CacheKey: "s1", PackID: "pack-1", LocalPath: "/cache/stickers/s1.webp"}, "")
	updated := c.expectUpsert(sub, "s1")
	if got := updated["item"].(map[string]any)["local_path"]; got != "/cache/stickers/s1.webp" {
		t.Fatalf("local_path after download = %v", got)
	}
}

func TestStickerPackViewInvalidatesOnFavoriteAndRecencyChanges(t *testing.T) {
	socketPath, daemon, db, _ := startStickerProtocolServer(t)
	seedStickerPack(t, db, store.StickerPack{ID: "pack-1", Name: "Pack One", StickerCount: 1, ContentsFetchedAt: 1})
	if err := db.UpsertPackSticker(context.Background(), store.Sticker{CacheKey: "s1", MimeType: "image/webp", PackID: "pack-1", PackOrder: 1}); err != nil {
		t.Fatal(err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"sticker_pack","pack_id":"pack-1"}`)
	c.expectUpsert(sub, "s1")
	c.expectReady(sub, true)

	if err := db.SetStickerFavorite(context.Background(), store.Sticker{CacheKey: "s1", MimeType: "image/webp"}, true, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	daemon.PublishStickerLibraryChanged(app.StickerSourceFavorite)
	favorite := c.expectUpsert(sub, "s1")
	if !itemBool(t, favorite, "is_favorite") {
		t.Fatalf("pack sticker is_favorite after update = false")
	}

	if err := db.TouchRecentSticker(context.Background(), store.Sticker{CacheKey: "s1", MimeType: "image/webp", LastUsed: 20, RecentWeight: 4.5}); err != nil {
		t.Fatal(err)
	}
	daemon.PublishStickerLibraryChanged(app.StickerSourceRecent)
	recent := c.expectUpsert(sub, "s1")
	item := recent["item"].(map[string]any)
	if item["last_used_unix"] != float64(20) || item["weight"] != 4.5 {
		t.Fatalf("pack sticker recency after update = %v", item)
	}
}

func TestStickerPackViewRequiresPackID(t *testing.T) {
	socketPath, _, _ := startChatsTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"sticker_pack"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("missing pack_id error code = %q, want %q", code, CodeInvalidParams)
	}
}

func TestStickerPackViewMissingPack(t *testing.T) {
	socketPath, _, _ := startChatsTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(fmt.Sprintf(`{"id":2,"method":"subscribe","params":{"view":"sticker_pack","pack_id":%q}}`, "missing"))
	if code := errorCode(t, c.recv()); code != CodeNotFound {
		t.Fatalf("missing pack error code = %q, want %q", code, CodeNotFound)
	}
}
