package protocol

import (
	"context"
	"testing"
	"time"

	"whatevrd/internal/store"
)

// The status view serves stored statuses newest-first with sender, fallback
// and media facts, and marks nothing viewed on its own.
func TestStatusViewServesStoredStatuses(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	if _, _, err := db.SaveStatusUpdate(context.Background(), store.StatusUpdateInput{
		ID:        "status:old",
		SenderID:  "peer@s.whatsapp.net",
		Timestamp: time.Unix(1_700_000_000, 0),
		Kind:      "text",
		Text:      "good morning",
	}); err != nil {
		t.Fatalf("seed text status: %v", err)
	}
	if _, _, err := db.SaveStatusUpdate(context.Background(), store.StatusUpdateInput{
		ID:            "status:new",
		SenderID:      "peer@s.whatsapp.net",
		SenderName:    "Peer",
		Timestamp:     time.Unix(1_700_000_100, 0),
		Kind:          store.MediaKindImage,
		MediaKind:     store.MediaKindImage,
		MediaMimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("seed photo status: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"status"}`)
	first := c.expectUpsert(sub, "status:new")["item"].(map[string]any)
	if first["kind"] != "image" {
		t.Fatalf("kind = %v, want image", first["kind"])
	}
	if first["fallback"] != "📷 Photo status" {
		t.Fatalf("fallback = %v, want photo status label", first["fallback"])
	}
	sender := first["sender"].(map[string]any)
	if sender["id"] != "peer@s.whatsapp.net" || sender["name"] != "Peer" {
		t.Fatalf("sender = %v, want peer/Peer", sender)
	}
	second := c.expectUpsert(sub, "status:old")["item"].(map[string]any)
	if second["fallback"] != "good morning" {
		t.Fatalf("fallback = %v, want the status text", second["fallback"])
	}
	c.expectReady(sub, true)
}

// The kept view lists one row per keep-enabled sender for the Status tab's
// archived sections.
func TestStatusKeptViewListsSenders(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	if err := db.SetStatusKeepSender(context.Background(), "peer@s.whatsapp.net", true); err != nil {
		t.Fatalf("keep sender: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"status.kept"}`)
	first := c.expectUpsert(sub, "peer@s.whatsapp.net")["item"].(map[string]any)
	if first["id"] != "peer@s.whatsapp.net" {
		t.Fatalf("kept item = %v, want the sender id", first)
	}
	c.expectReady(sub, true)
}

// The muted view lists one row per muted sender for the Status tab's Muted
// section.
func TestStatusMutedViewListsSenders(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	if err := db.SetStatusMutedSender(context.Background(), "peer@s.whatsapp.net", true); err != nil {
		t.Fatalf("mute sender: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"status.muted"}`)
	first := c.expectUpsert(sub, "peer@s.whatsapp.net")["item"].(map[string]any)
	if first["id"] != "peer@s.whatsapp.net" {
		t.Fatalf("muted item = %v, want the sender id", first)
	}
	c.expectReady(sub, true)
}
