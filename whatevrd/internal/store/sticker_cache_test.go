package store

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
)

func TestEnsureStickerCacheKeysBackfillsLegacyRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "whatevrd.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	fileSHA := []byte{1, 2, 3}
	payload, err := proto.Marshal(&waE2E.StickerMessage{FileSHA256: fileSHA})
	if err != nil {
		t.Fatalf("marshal sticker: %v", err)
	}

	if _, err := db.SaveMediaMessage(ctx, MediaMessageInput{
		TextMessageInput: TextMessageInput{
			ID:        "chat-1:sticker-1",
			ChatID:    "chat-1",
			ChatName:  "Chat One",
			SenderID:  "sender-1",
			Timestamp: time.Unix(100, 0),
		},
		MediaKind:     MediaKindSticker,
		MediaMimeType: "image/webp",
		MediaPayload:  payload,
	}); err != nil {
		t.Fatalf("save sticker: %v", err)
	}

	// Simulate a row written before cache keys existed.
	if _, err := db.conn.ExecContext(ctx, `UPDATE messages SET media_cache_key = '' WHERE id = 'chat-1:sticker-1'`); err != nil {
		t.Fatalf("clear cache key: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()

	message, err := db.GetMessage(ctx, "chat-1:sticker-1")
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if want := hex.EncodeToString(fileSHA); message.MediaCacheKey != want {
		t.Fatalf("media_cache_key = %q, want backfilled %q", message.MediaCacheKey, want)
	}
}
