package wa

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func TestStaleMediaDownloadErrorIncludesForbidden(t *testing.T) {
	if !staleMediaDownloadError(whatsmeow.ErrMediaDownloadFailedWith403) {
		t.Fatal("403 media download error should request media retry")
	}
	if staleMediaDownloadError(context.Canceled) {
		t.Fatal("context cancellation should not request media retry")
	}
}

func TestDecodedImageDimensionsReadsImageConfig(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 12, 3))); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	width, height := decodedImageDimensions(buf.Bytes())
	if width != 12 || height != 3 {
		t.Fatalf("decoded dimensions = %dx%d, want 12x3", width, height)
	}

	width, height = decodedImageDimensions([]byte("not an image"))
	if width != 0 || height != 0 {
		t.Fatalf("invalid image dimensions = %dx%d, want 0x0", width, height)
	}
}

func TestRefreshedImagePayloadReplacesExpiredURL(t *testing.T) {
	payload, err := proto.Marshal(&waE2E.ImageMessage{
		URL:        proto.String("https://mmg.whatsapp.net/v/t62.7118-24/image.enc"),
		DirectPath: proto.String("/old/path.enc?ccb=11-4"),
		Mimetype:   proto.String("image/jpeg"),
	})
	if err != nil {
		t.Fatalf("marshal image: %v", err)
	}

	updatedPayload, err := refreshedMediaPayload(appstore.Message{
		MediaKind:    appstore.MediaKindImage,
		MediaPayload: payload,
	}, "/new/path.enc?ccb=11-4")
	if err != nil {
		t.Fatalf("refreshedMediaPayload() error = %v", err)
	}

	var image waE2E.ImageMessage
	if err := proto.Unmarshal(updatedPayload, &image); err != nil {
		t.Fatalf("unmarshal image: %v", err)
	}
	if got := image.GetDirectPath(); got != "/new/path.enc?ccb=11-4" {
		t.Fatalf("DirectPath = %q, want refreshed path", got)
	}
	if got := image.GetURL(); got != "" {
		t.Fatalf("URL = %q, want empty so direct path is used", got)
	}
}

func TestUpdateMessageMediaPayloadPersistsRefreshedPayload(t *testing.T) {
	ctx := context.Background()
	_, db := newTestMediaClient(t)
	defer db.Close()

	originalPayload, err := proto.Marshal(&waE2E.ImageMessage{
		URL:        proto.String("https://mmg.whatsapp.net/v/t62.7118-24/image.enc"),
		DirectPath: proto.String("/old/path.enc?ccb=11-4"),
		Mimetype:   proto.String("image/jpeg"),
	})
	if err != nil {
		t.Fatalf("marshal original image: %v", err)
	}
	if _, err := db.SaveMediaMessage(ctx, appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:        "chat-1:image-1",
			ChatID:    "chat-1",
			ChatName:  "Chat One",
			SenderID:  "sender-1",
			Timestamp: time.Unix(100, 0),
		},
		MediaKind:     appstore.MediaKindImage,
		MediaMimeType: "image/jpeg",
		MediaPayload:  originalPayload,
	}); err != nil {
		t.Fatalf("save image message: %v", err)
	}

	refreshedPayload, err := refreshedMediaPayload(appstore.Message{
		MediaKind:    appstore.MediaKindImage,
		MediaPayload: originalPayload,
	}, "/new/path.enc?ccb=11-4")
	if err != nil {
		t.Fatalf("refreshedMediaPayload() error = %v", err)
	}
	updated, err := db.UpdateMessageMediaPayload(ctx, "chat-1:image-1", refreshedPayload)
	if err != nil {
		t.Fatalf("UpdateMessageMediaPayload() error = %v", err)
	}
	if !bytes.Equal(updated.MediaPayload, refreshedPayload) {
		t.Fatal("updated message did not include refreshed payload")
	}

	loaded, err := db.GetMessage(ctx, "chat-1:image-1")
	if err != nil {
		t.Fatalf("GetMessage() error = %v", err)
	}
	if !bytes.Equal(loaded.MediaPayload, refreshedPayload) {
		t.Fatal("stored message did not persist refreshed payload")
	}
}

func TestDownloadableStickerIgnoresPlaceholderURLWhenDirectPathExists(t *testing.T) {
	payload, err := proto.Marshal(&waE2E.StickerMessage{
		URL:        proto.String("https://a.whatsapp.net"),
		DirectPath: proto.String("/v/t62.15575-24/sticker.enc?ccb=11-4"),
	})
	if err != nil {
		t.Fatalf("marshal sticker: %v", err)
	}

	media, err := downloadableMediaMessage(appstore.Message{
		MediaKind:    appstore.MediaKindSticker,
		MediaPayload: payload,
	})
	if err != nil {
		t.Fatalf("downloadableMediaMessage() error = %v", err)
	}

	if got := media.GetURL(); got != "" {
		t.Fatalf("GetURL() = %q, want empty placeholder override", got)
	}
	if got := media.GetDirectPath(); got == "" {
		t.Fatal("GetDirectPath() is empty, want original direct path preserved")
	}
}

func TestDownloadableStickerKeepsNonPlaceholderURL(t *testing.T) {
	const stickerURL = "https://mmg.whatsapp.net/v/t62.15575-24/sticker.enc"
	payload, err := proto.Marshal(&waE2E.StickerMessage{
		URL:        proto.String(stickerURL),
		DirectPath: proto.String("/v/t62.15575-24/sticker.enc?ccb=11-4"),
	})
	if err != nil {
		t.Fatalf("marshal sticker: %v", err)
	}

	media, err := downloadableMediaMessage(appstore.Message{
		MediaKind:    appstore.MediaKindSticker,
		MediaPayload: payload,
	})
	if err != nil {
		t.Fatalf("downloadableMediaMessage() error = %v", err)
	}

	if got := media.GetURL(); got != stickerURL {
		t.Fatalf("GetURL() = %q, want %q", got, stickerURL)
	}
}

func TestDownloadMessageMediaReusesDownloadedSticker(t *testing.T) {
	ctx := context.Background()
	client, db := newTestMediaClient(t)
	defer db.Close()

	payload := testStickerPayload(t, []byte{1, 2, 3}, []byte{4, 5, 6})
	cachedPath := filepath.Join(t.TempDir(), "cached.webp")
	if err := os.WriteFile(cachedPath, []byte("webp"), 0o600); err != nil {
		t.Fatalf("write cached sticker: %v", err)
	}
	saveTestSticker(t, db, "chat-1:existing", payload, cachedPath)
	saveTestSticker(t, db, "chat-1:current", payload, "")

	message, err := client.DownloadMessageMedia(ctx, "chat-1:current")
	if err != nil {
		t.Fatalf("DownloadMessageMedia() error = %v", err)
	}
	if message.MediaLocalPath != cachedPath {
		t.Fatalf("MediaLocalPath = %q, want cached path %q", message.MediaLocalPath, cachedPath)
	}
}

func TestDownloadMessageMediaUsesContentAddressedStickerCache(t *testing.T) {
	ctx := context.Background()
	client, db := newTestMediaClient(t)
	defer db.Close()

	fileSHA := []byte{0x0a, 0x0b, 0x0c}
	payload := testStickerPayload(t, fileSHA, []byte{4, 5, 6})
	stickerDir := filepath.Join(client.paths.MediaCacheDir, "stickers")
	if err := os.MkdirAll(stickerDir, 0o700); err != nil {
		t.Fatalf("create sticker dir: %v", err)
	}
	cachedPath := filepath.Join(stickerDir, "0a0b0c.webp")
	if err := os.WriteFile(cachedPath, []byte("webp"), 0o600); err != nil {
		t.Fatalf("write cached sticker: %v", err)
	}
	saveTestSticker(t, db, "chat-1:current", payload, "")

	message, err := client.DownloadMessageMedia(ctx, "chat-1:current")
	if err != nil {
		t.Fatalf("DownloadMessageMedia() error = %v", err)
	}
	if message.MediaLocalPath != cachedPath {
		t.Fatalf("MediaLocalPath = %q, want content-addressed path %q", message.MediaLocalPath, cachedPath)
	}
}

func TestResolveCachedStickerMediaUpdatesReturnedMessages(t *testing.T) {
	ctx := context.Background()
	client, db := newTestMediaClient(t)
	defer db.Close()

	payload := testStickerPayload(t, []byte{9, 8, 7}, []byte{6, 5, 4})
	cachedPath := filepath.Join(t.TempDir(), "cached.webp")
	if err := os.WriteFile(cachedPath, []byte("webp"), 0o600); err != nil {
		t.Fatalf("write cached sticker: %v", err)
	}
	saveTestSticker(t, db, "chat-1:existing", payload, cachedPath)
	current := saveTestSticker(t, db, "chat-1:current", payload, "")

	messages := client.ResolveCachedStickerMedia(ctx, []appstore.Message{current.Message})
	if got := messages[0].MediaLocalPath; got != cachedPath {
		t.Fatalf("resolved MediaLocalPath = %q, want %q", got, cachedPath)
	}
}

func newTestMediaClient(t *testing.T) (*Client, *appstore.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	return &Client{
		store:          db,
		paths:          app.Paths{MediaCacheDir: cacheDir},
		daemon:         app.NewDaemon(app.Paths{}),
		mediaDownloads: make(map[string]*mediaDownloadState),
		mediaRetries:   make(map[string]*mediaRetryState),
	}, db
}

func testStickerPayload(t *testing.T, fileSHA, fileEncSHA []byte) []byte {
	t.Helper()
	payload, err := proto.Marshal(&waE2E.StickerMessage{
		URL:           proto.String("https://a.whatsapp.net"),
		DirectPath:    proto.String("/v/t62.15575-24/sticker.enc?ccb=11-4"),
		FileSHA256:    fileSHA,
		FileEncSHA256: fileEncSHA,
		Mimetype:      proto.String("image/webp"),
	})
	if err != nil {
		t.Fatalf("marshal sticker: %v", err)
	}
	return payload
}

func saveTestSticker(t *testing.T, db *appstore.DB, id string, payload []byte, localPath string) appstore.SavedTextMessage {
	t.Helper()
	saved, err := db.SaveMediaMessage(context.Background(), appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:        id,
			ChatID:    "chat-1",
			ChatName:  "Chat One",
			SenderID:  "sender-1",
			Timestamp: time.Unix(100, 0),
		},
		MediaKind:      appstore.MediaKindSticker,
		MediaMimeType:  "image/webp",
		MediaLocalPath: localPath,
		MediaPayload:   payload,
	})
	if err != nil {
		t.Fatalf("save sticker %s: %v", id, err)
	}
	return saved
}
