package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestListMessagesForExportOldestFirst locks in that exports read the whole
// chat oldest-first regardless of insert order.
func TestListMessagesForExportOldestFirst(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	seed := func(id string, at int64) {
		if _, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:        id,
			ChatID:    "chat-1",
			ChatName:  "Export Chat",
			SenderID:  "peer",
			Text:      id,
			Timestamp: time.Unix(at, 0),
			Direction: DirectionIncoming,
			Status:    StatusDelivered,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("chat-1:m3", 300)
	seed("chat-1:m1", 100)
	seed("chat-1:m2", 200)

	messages, err := db.ListMessagesForExport(ctx, "chat-1")
	if err != nil {
		t.Fatalf("list for export: %v", err)
	}
	if len(messages) != 3 || messages[0].ID != "chat-1:m1" || messages[1].ID != "chat-1:m2" || messages[2].ID != "chat-1:m3" {
		ids := make([]string, 0, len(messages))
		for _, message := range messages {
			ids = append(ids, message.ID)
		}
		t.Fatalf("export order = %v; want [m1 m2 m3]", ids)
	}
}
