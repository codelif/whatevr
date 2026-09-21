package wa

import (
	"context"
	"path/filepath"
	"testing"

	waLog "go.mau.fi/whatsmeow/util/log"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// TestInboundStickerEntersRecents locks in cross-device recents: a sticker
// arriving live from another device must land in the local Recents library
// (the phone lists it in its own recents), while own sends and non-stickers
// stay out of this path.
func TestInboundStickerEntersRecents(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	client := &Client{store: db, daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}

	inbound := appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:        "chat:m1",
			Direction: appstore.DirectionIncoming,
		},
		MediaKind:     appstore.MediaKindSticker,
		MediaMimeType: "image/webp",
		MediaCacheKey: "aabbcc",
	}
	client.recordInboundStickerRecency(ctx, inbound)

	recents, err := db.ListRecentStickers(ctx, 10)
	if err != nil {
		t.Fatalf("list recents: %v", err)
	}
	if len(recents) != 1 || recents[0].CacheKey != "aabbcc" {
		t.Fatalf("recents = %+v, want the inbound sticker", recents)
	}

	// Own sends are recorded by SendSticker, not here; other kinds never.
	outbound := inbound
	outbound.Direction = appstore.DirectionOutgoing
	outbound.MediaCacheKey = "outbound"
	client.recordInboundStickerRecency(ctx, outbound)
	nonSticker := inbound
	nonSticker.MediaKind = appstore.MediaKindImage
	nonSticker.MediaCacheKey = "imagekey"
	client.recordInboundStickerRecency(ctx, nonSticker)

	recents, err = db.ListRecentStickers(ctx, 10)
	if err != nil {
		t.Fatalf("list recents: %v", err)
	}
	if len(recents) != 1 {
		t.Fatalf("recents = %+v, want only the inbound sticker", recents)
	}
}
