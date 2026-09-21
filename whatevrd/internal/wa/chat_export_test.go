package wa

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appstore "whatevrd/internal/store"
)

// TestExportChatOfficialFormat locks in the WhatsApp .txt export shape:
// encryption notice first, M/D/YY clock stamps, "You" for outgoing, display
// names (phone fallback) otherwise, raw markup preserved, media as
// "<Media omitted>", revoked rows dropped.
func TestExportChatOfficialFormat(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()

	save := func(id, sender, name, text, kind, direction string, at int64) {
		input := appstore.TextMessageInput{
			ID:         id,
			ChatID:     "chat-1",
			ChatName:   "Export Chat",
			SenderID:   sender,
			SenderName: name,
			Text:       text,
			Timestamp:  time.Unix(at, 0),
			Direction:  direction,
			Status:     appstore.StatusDelivered,
		}
		if kind != "" {
			if _, err := client.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
				TextMessageInput: input,
				MediaKind:        kind,
				MediaMimeType:    "image/jpeg",
			}); err != nil {
				t.Fatalf("save media %s: %v", id, err)
			}
			return
		}
		if _, err := client.store.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save text %s: %v", id, err)
		}
	}
	// 2024-05-07 09:50:00 local wall clock in the assertions below is derived
	// from the same layout, so the test holds in any timezone.
	at := time.Date(2024, 5, 7, 9, 50, 0, 0, time.Local).Unix()
	save("chat-1:m1", "peer", "athul", "*Malappuram* news\nsecond line", "", appstore.DirectionIncoming, at)
	save("chat-1:m2", "me", "", "noted", "", appstore.DirectionOutgoing, at+60)
	save("chat-1:m3", "peer", "", "", appstore.MediaKindImage, appstore.DirectionIncoming, at+120)
	save("chat-1:m4", "5550001", "", "gone", "", appstore.DirectionIncoming, at+180)
	if _, _, _, err := client.store.MarkMessageRevoked(ctx, "chat-1:m4", false); err != nil {
		t.Fatalf("revoke m4: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "export.txt")
	path, err := client.ExportChat(ctx, "chat-1", dest)
	if err != nil {
		t.Fatalf("export chat: %v", err)
	}
	if path != dest {
		t.Fatalf("export path = %q, want %q", path, dest)
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	stamp := func(at int64) string {
		return time.Unix(at, 0).Local().Format(exportTimestampLayout)
	}
	var want strings.Builder
	want.WriteString("Messages and calls are end-to-end encrypted. Only people in this chat can read, listen to, or share them.\n")
	want.WriteString(stamp(at) + " - athul: *Malappuram* news\nsecond line\n")
	want.WriteString(stamp(at+60) + " - You: noted\n")
	// Sender names resolve through the senders table, so m3 (saved nameless
	// for the same sender as m1) still prints "athul".
	want.WriteString(stamp(at+120) + " - athul: <Media omitted>\n")
	if got := string(raw); got != want.String() {
		t.Fatalf("export transcript mismatch:\ngot:\n%s\nwant:\n%s", got, want.String())
	}
}

// TestExportChatRejectsBadInput locks in validation: unknown chats 404 and
// relative destinations are rejected.
func TestExportChatRejectsBadInput(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()

	if _, err := client.ExportChat(ctx, "nope", filepath.Join(t.TempDir(), "x.txt")); err == nil {
		t.Fatal("expected error for unknown chat")
	}
	if _, err := client.ExportChat(ctx, "chat-1", "relative/path.txt"); err == nil {
		t.Fatal("expected error for relative destination")
	}
}
