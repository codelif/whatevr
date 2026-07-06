package protocol

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// startChatsTestServer starts a protocol server backed by a real store and
// returns the socket path, the daemon (to publish invalidating events) and the
// store (to mutate chat state).
func startChatsTestServer(t *testing.T) (string, *app.Daemon, *store.DB) {
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
	RegisterDaemonViews(server, daemon, db)

	return socketPath, daemon, db
}

func seedChat(t *testing.T, db *store.DB, id string, isGroup bool, ts time.Time) {
	t.Helper()
	// The message id embeds the timestamp so re-seeding the same chat with a
	// newer message inserts a fresh row (ON CONFLICT DO NOTHING would
	// otherwise skip the chat's last-message bump).
	msgID := fmt.Sprintf("m-%s-%d", id, ts.Unix())
	if _, err := db.SaveTextMessage(context.Background(), store.TextMessageInput{
		ID:        msgID,
		ChatID:    id,
		Text:      "hi",
		Timestamp: ts,
		IsGroup:   isGroup,
	}); err != nil {
		t.Fatalf("seed chat %s: %v", id, err)
	}
}

func TestChatsViewInitialOrderAndSortKeys(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	seedChat(t, db, "old@s.whatsapp.net", false, base)
	seedChat(t, db, "new@s.whatsapp.net", false, base.Add(2*time.Minute))
	seedChat(t, db, "mid@s.whatsapp.net", false, base.Add(time.Minute))

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"chats"}`)

	// Newest first, and every upsert carries a strictly ascending sort key.
	prev := ""
	for _, id := range []string{"new@s.whatsapp.net", "mid@s.whatsapp.net", "old@s.whatsapp.net"} {
		msg := c.expectUpsert(sub, id)
		sortKey, ok := msg["sort"].(string)
		if !ok || sortKey == "" {
			t.Fatalf("upsert %s missing sort: %v", id, msg)
		}
		if sortKey <= prev {
			t.Fatalf("sort not ascending: %q after %q", sortKey, prev)
		}
		prev = sortKey
		item := msg["item"].(map[string]any)
		if item["id"] != id {
			t.Fatalf("item.id = %v, want %s", item["id"], id)
		}
	}
	c.expectReady(sub, true)
}

func TestChatsViewFilterAndArchived(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	seedChat(t, db, "direct@s.whatsapp.net", false, base.Add(time.Minute))
	seedChat(t, db, "group@g.us", true, base)

	c := dialTest(t, socketPath)
	c.hello()

	// direct filter shows only the direct chat.
	sub := c.subscribe(2, `{"view":"chats","filter":"direct"}`)
	c.expectUpsert(sub, "direct@s.whatsapp.net")
	c.expectReady(sub, true)

	// groups filter shows only the group.
	sub2 := c.subscribe(3, `{"view":"chats","filter":"groups"}`)
	c.expectUpsert(sub2, "group@g.us")
	c.expectReady(sub2, true)
}

func TestChatsViewLivePinAndArchive(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	seedChat(t, db, "a@s.whatsapp.net", false, base.Add(2*time.Minute))
	seedChat(t, db, "b@s.whatsapp.net", false, base.Add(time.Minute))

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"chats"}`)
	first := c.expectUpsert(sub, "a@s.whatsapp.net")
	c.expectUpsert(sub, "b@s.whatsapp.net")
	c.expectReady(sub, true)

	// Pin the older chat: it re-upserts with a pinned-section sort that sorts
	// ahead of the unpinned chat.
	chat, _, err := db.UpdateChatPinState(ctx, "b@s.whatsapp.net", true, 1)
	if err != nil {
		t.Fatalf("pin b: %v", err)
	}
	daemon.PublishChatUpdated(toTestAppChat(chat))
	moved := c.expectUpsert(sub, "b@s.whatsapp.net")
	if moved["sort"].(string) >= first["sort"].(string) {
		t.Fatalf("pinned chat sort %q not ahead of unpinned %q", moved["sort"], first["sort"])
	}
	item := moved["item"].(map[string]any)
	if item["pinned"] != true {
		t.Fatalf("pinned flag not set: %v", item)
	}

	// Archiving removes it from the unarchived list.
	chat, _, err = db.UpdateChatArchiveState(ctx, "b@s.whatsapp.net", true)
	if err != nil {
		t.Fatalf("archive b: %v", err)
	}
	daemon.PublishChatUpdated(toTestAppChat(chat))
	c.expectRemove(sub, "b@s.whatsapp.net")
}

func TestChatsViewWindowRemoveOnFallOut(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	seedChat(t, db, "a@s.whatsapp.net", false, base.Add(time.Minute))
	seedChat(t, db, "b@s.whatsapp.net", false, base)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"chats","limit":1}`)
	c.expectUpsert(sub, "a@s.whatsapp.net")
	c.expectReady(sub, false) // b exists beyond the window

	// A new message in b makes it the most recent, pushing a out of the
	// size-1 window: b enters, a is removed.
	seedChat(t, db, "b@s.whatsapp.net", false, base.Add(2*time.Minute))
	chat, err := db.GetChat(ctx, "b@s.whatsapp.net")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	daemon.PublishNewMessage(app.Message{ID: "m2", ChatID: "b@s.whatsapp.net"}, toTestAppChat(chat))
	c.expectUpsert(sub, "b@s.whatsapp.net")
	c.expectRemove(sub, "a@s.whatsapp.net")
}

func TestChatsViewRejectsBadFilter(t *testing.T) {
	socketPath, _, _ := startChatsTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"chats","filter":"bogus"}}`)
	msg := c.recv()
	errObj, ok := msg["error"].(map[string]any)
	if !ok || errObj["code"] != string(CodeInvalidParams) {
		t.Fatalf("expected invalid_params for bad filter, got %v", msg)
	}
}

// toTestAppChat mirrors the daemon's store→app chat projection closely enough
// for the view (which re-reads the store) to invalidate on the event.
func toTestAppChat(c store.Chat) app.Chat {
	return app.Chat{
		ID:               c.ID,
		Name:             c.Name,
		IsGroup:          c.IsGroup,
		IsPinned:         c.IsPinned,
		PinnedOrder:      c.PinnedOrder,
		IsArchived:       c.IsArchived,
		LastMessageTime:  c.LastMessageTime,
		HistoryExhausted: c.HistoryExhausted,
	}
}
