package wa

import (
	"bytes"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	appstore "whatevrd/internal/store"
)

// An empty BLOB scanned from SQLite comes back as a non-nil []byte{}. If that
// slice is assigned straight into InitialHistBootstrapInlinePayload, whatmeow's
// DownloadHistorySync takes the inline branch with zero bytes and fails in zlib
// with "unexpected EOF" instead of downloading the media. The field must stay nil
// for download-based chunks.
func TestHistorySyncNotificationFromChunkEmptyInlinePayloadStaysNil(t *testing.T) {
	chunk := appstore.HistorySyncChunk{
		ID:            "AC674C78",
		SyncType:      int32(waE2E.HistorySyncType_RECENT),
		ChunkOrder:    1,
		Progress:      3,
		FileLength:    764941,
		DirectPath:    "/v/t62.7117-24/foo.enc",
		MediaKey:      bytes.Repeat([]byte{0x01}, 32),
		FileSHA256:    bytes.Repeat([]byte{0x02}, 32),
		FileEncSHA256: bytes.Repeat([]byte{0x03}, 32),
		EncHandle:     "enc-handle",
		InlinePayload: []byte{}, // non-nil but empty, as scanned from an empty BLOB
	}

	notif := historySyncNotificationFromChunk(chunk)

	if notif.InitialHistBootstrapInlinePayload != nil {
		t.Fatalf("expected nil inline payload for download-based chunk, got %d bytes", len(notif.InitialHistBootstrapInlinePayload))
	}
	if got := notif.GetDirectPath(); got != chunk.DirectPath {
		t.Fatalf("DirectPath = %q, want %q", got, chunk.DirectPath)
	}
	if got := notif.GetEncHandle(); got != chunk.EncHandle {
		t.Fatalf("EncHandle = %q, want %q", got, chunk.EncHandle)
	}
	if !bytes.Equal(notif.GetMediaKey(), chunk.MediaKey) {
		t.Fatal("MediaKey did not round-trip")
	}
	if !bytes.Equal(notif.GetFileSHA256(), chunk.FileSHA256) {
		t.Fatal("FileSHA256 did not round-trip")
	}
	if !bytes.Equal(notif.GetFileEncSHA256(), chunk.FileEncSHA256) {
		t.Fatal("FileEncSHA256 did not round-trip")
	}
	if got := notif.GetFileLength(); got != chunk.FileLength {
		t.Fatalf("FileLength = %d, want %d", got, chunk.FileLength)
	}
}

func TestHistorySyncNotificationFromChunkPreservesInlinePayload(t *testing.T) {
	payload := []byte("compressed-history-bytes")
	chunk := appstore.HistorySyncChunk{
		ID:            "AC10FC72",
		SyncType:      int32(waE2E.HistorySyncType_INITIAL_BOOTSTRAP),
		InlinePayload: payload,
	}

	notif := historySyncNotificationFromChunk(chunk)

	if !bytes.Equal(notif.GetInitialHistBootstrapInlinePayload(), payload) {
		t.Fatal("inline payload was not preserved for inline chunk")
	}
}
