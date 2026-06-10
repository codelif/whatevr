package wa

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waSyncAction "go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func newTestStickerClient(t *testing.T) (*Client, *appstore.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &Client{
		store:            db,
		paths:            app.Paths{MediaCacheDir: filepath.Join(t.TempDir(), "cache")},
		daemon:           app.NewDaemon(app.Paths{}),
		log:              waLog.Noop,
		stickerDownloads: make(map[string]*stickerFileDownloadState),
	}, db
}

func TestIngestRecentStickersSeedsRecents(t *testing.T) {
	c, db := newTestStickerClient(t)
	ctx := context.Background()

	fileSHA := []byte{1, 2, 3, 4}
	c.ingestRecentStickers(ctx, []*waHistorySync.StickerMetadata{
		nil, // tolerated
		{
			URL:               proto.String("https://mmg.whatsapp.net/sticker.enc"),
			DirectPath:        proto.String("/v/sticker.enc"),
			FileSHA256:        fileSHA,
			FileEncSHA256:     []byte{9, 9},
			MediaKey:          []byte{7},
			Mimetype:          proto.String("image/webp"),
			Width:             proto.Uint32(512),
			Height:            proto.Uint32(512),
			Weight:            proto.Float32(4.5),
			LastStickerSentTS: proto.Int64(1_700_000_000_000), // milliseconds
		},
		{FileSHA256: nil}, // no hash: skipped
	})

	recents, err := db.ListRecentStickers(ctx, 10)
	if err != nil {
		t.Fatalf("list recents: %v", err)
	}
	if len(recents) != 1 {
		t.Fatalf("recents = %d, want 1", len(recents))
	}
	s := recents[0]
	if s.CacheKey != hex.EncodeToString(fileSHA) {
		t.Fatalf("cache key = %q", s.CacheKey)
	}
	if s.LastUsed != 1_700_000_000 {
		t.Fatalf("last used = %d, want seconds-normalized", s.LastUsed)
	}
	if s.RecentWeight != 4.5 || len(s.StickerPayload) == 0 {
		t.Fatalf("metadata not stored: %+v", s)
	}
}

func TestHandleStickerAppStateFavoriteRoundTrip(t *testing.T) {
	c, db := newTestStickerClient(t)
	ctx := context.Background()

	encHash := []byte{0xAA, 0xBB}
	favorite := &events.AppState{
		Index: []string{"favoriteSticker", "some-id"},
		SyncActionValue: &waSyncAction.SyncActionValue{
			Timestamp: proto.Int64(1_700_000_111_000),
			StickerAction: &waSyncAction.StickerAction{
				URL:           proto.String("https://mmg.whatsapp.net/fav.enc"),
				DirectPath:    proto.String("/v/fav.enc"),
				FileEncSHA256: encHash,
				MediaKey:      []byte{1},
				Mimetype:      proto.String("image/webp"),
				Width:         proto.Uint32(512),
				Height:        proto.Uint32(512),
				IsFavorite:    proto.Bool(true),
			},
		},
	}
	c.handleStickerAppState(ctx, favorite)

	favorites, err := db.ListFavoriteStickers(ctx, 10)
	if err != nil || len(favorites) != 1 {
		t.Fatalf("favorites = %d (%v), want 1", len(favorites), err)
	}
	encKey := hex.EncodeToString(encHash)
	if favorites[0].CacheKey != "enc:"+encKey || favorites[0].EncCacheKey != encKey {
		t.Fatalf("favorite keys wrong: %+v", favorites[0])
	}

	// Unfavorite via a SET mutation with IsFavorite=false.
	favorite.SyncActionValue.StickerAction.IsFavorite = proto.Bool(false)
	c.handleStickerAppState(ctx, favorite)
	favorites, err = db.ListFavoriteStickers(ctx, 10)
	if err != nil || len(favorites) != 0 {
		t.Fatalf("favorites after unfavorite = %d (%v), want 0", len(favorites), err)
	}
}

func TestReconcileFavoriteStickersReplacesSet(t *testing.T) {
	c, db := newTestStickerClient(t)
	ctx := context.Background()

	// Pre-existing favorite that the snapshot no longer contains.
	if err := db.SetStickerFavorite(ctx, appstore.Sticker{CacheKey: "enc:dead", EncCacheKey: "dead"}, true, time.Unix(100, 0)); err != nil {
		t.Fatalf("seed favorite: %v", err)
	}

	snapshot := []any{
		&events.AppState{
			Index: []string{"favoriteSticker", "x"},
			SyncActionValue: &waSyncAction.SyncActionValue{
				Timestamp: proto.Int64(1_700_000_222_000),
				StickerAction: &waSyncAction.StickerAction{
					FileEncSHA256: []byte{0xCA, 0xFE},
					Mimetype:      proto.String("image/webp"),
					IsFavorite:    proto.Bool(true),
				},
			},
		},
		&events.AppState{Index: []string{"pin_v1", "x"}}, // unrelated index ignored
	}
	c.reconcileFavoriteStickersFromEvents(ctx, snapshot)

	favorites, err := db.ListFavoriteStickers(ctx, 10)
	if err != nil || len(favorites) != 1 {
		t.Fatalf("favorites = %d (%v), want 1", len(favorites), err)
	}
	if favorites[0].EncCacheKey != "cafe" {
		t.Fatalf("favorite = %+v, want cafe", favorites[0])
	}
}

func TestReconcileFavoriteStickersNoopWithoutMutations(t *testing.T) {
	c, db := newTestStickerClient(t)
	ctx := context.Background()

	if err := db.SetStickerFavorite(ctx, appstore.Sticker{CacheKey: "enc:dead", EncCacheKey: "dead"}, true, time.Unix(100, 0)); err != nil {
		t.Fatalf("seed favorite: %v", err)
	}
	// Snapshot with no favoriteSticker mutations must not clear favorites.
	c.reconcileFavoriteStickersFromEvents(ctx, []any{
		&events.AppState{Index: []string{"pin_v1", "x"}},
	})
	favorites, err := db.ListFavoriteStickers(ctx, 10)
	if err != nil || len(favorites) != 1 {
		t.Fatalf("favorites = %d (%v), want 1 (untouched)", len(favorites), err)
	}
}

func TestNormalizeUnixTimestamp(t *testing.T) {
	if got := normalizeUnixTimestamp(1_700_000_000); got != 1_700_000_000 {
		t.Fatalf("seconds passthrough = %d", got)
	}
	if got := normalizeUnixTimestamp(1_700_000_000_000); got != 1_700_000_000 {
		t.Fatalf("milliseconds = %d", got)
	}
	if got := normalizeUnixTimestamp(-5); got != 0 {
		t.Fatalf("negative = %d", got)
	}
}
