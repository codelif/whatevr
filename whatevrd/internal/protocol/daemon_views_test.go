package protocol

import (
	"context"
	"sync"
	"testing"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

type fakePendingCounter struct {
	mu    sync.Mutex
	count int
}

func (f *fakePendingCounter) CountPendingOutgoingMessages(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count, nil
}

// fakePendingCounter satisfies DaemonStore so it can register the daemon
// views; the chats and messages views are unused in these object-view tests.
func (f *fakePendingCounter) ListChatsForView(context.Context, store.ChatListFilter) ([]store.Chat, error) {
	return nil, nil
}

func (f *fakePendingCounter) ListMessages(context.Context, string, int, string) ([]store.Message, error) {
	return nil, nil
}

func (f *fakePendingCounter) ListMessagesAfter(context.Context, string, int, string) ([]store.Message, error) {
	return nil, nil
}

func (f *fakePendingCounter) ListMessagesAround(context.Context, string, int, string) ([]store.Message, error) {
	return nil, nil
}

func (f *fakePendingCounter) GetMessage(context.Context, string) (store.Message, error) {
	return store.Message{}, nil
}

func (f *fakePendingCounter) ListMessagesAroundUnread(context.Context, string, int, int) ([]store.Message, string, error) {
	return nil, "", nil
}

func (f *fakePendingCounter) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}

func (f *fakePendingCounter) SenderDisplay(context.Context, string) (string, string, error) {
	return "", "", nil
}

func (f *fakePendingCounter) ListStarredMessages(context.Context, string, int, string) ([]store.StarredMessage, error) {
	return nil, nil
}

func (f *fakePendingCounter) ListPinnedMessages(context.Context, string) ([]store.Message, error) {
	return nil, nil
}

func (f *fakePendingCounter) set(count int) {
	f.mu.Lock()
	f.count = count
	f.mu.Unlock()
}

func TestConnectionViewInitialAndLiveUpdates(t *testing.T) {
	socketPath, server := startTestServer(t)
	pending := &fakePendingCounter{count: 2}
	RegisterDaemonViews(server, server.daemon, pending, nil)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"connection"}`)
	msg := c.expectUpsert(sub, "self")
	item := msg["item"].(map[string]any)
	if msg["sort"] != objectViewSort || item["state"] != "online" || item["pending_outgoing_count"] != float64(2) {
		t.Fatalf("initial connection item = %v", msg)
	}
	c.expectReady(sub, true)

	server.daemon.SetConnMeta(3, 12345, true)
	server.daemon.SetStateDetail(app.StateOffline, "network down")
	msg = c.expectUpsert(sub, "self")
	item = msg["item"].(map[string]any)
	if item["state"] != "offline" || item["detail"] != "network down" || item["retry_attempt"] != float64(3) || item["next_retry_unix"] != float64(12345) || item["can_reconnect"] != true {
		t.Fatalf("live connection item = %v", msg)
	}

	pending.set(1)
	server.daemon.PublishMessageUpdated(app.Message{ID: "m1"})
	msg = c.expectUpsert(sub, "self")
	item = msg["item"].(map[string]any)
	if item["pending_outgoing_count"] != float64(1) {
		t.Fatalf("pending count item = %v", msg)
	}
}

func TestLoginViewStateQRAndExpiry(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.daemon.SetStateDetail(app.StateNeedLogin, "scan a code")
	RegisterDaemonViews(server, server.daemon, nil, nil)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"login"}`)
	msg := c.expectUpsert(sub, "self")
	item := msg["item"].(map[string]any)
	if item["state"] != "need_login" || item["detail"] != "scan a code" {
		t.Fatalf("initial login item = %v", msg)
	}
	if _, ok := item["qr"]; ok {
		t.Fatalf("login item should not have QR before one is published: %v", msg)
	}
	c.expectReady(sub, true)

	server.daemon.PublishQRCode("qr-code", time.Now().Add(200*time.Millisecond))
	msg = c.expectUpsert(sub, "self")
	item = msg["item"].(map[string]any)
	qr, ok := item["qr"].(map[string]any)
	if !ok || qr["code"] != "qr-code" || qr["expires_at"] == "" {
		t.Fatalf("QR login item = %v", msg)
	}

	msg = c.expectUpsert(sub, "self")
	item = msg["item"].(map[string]any)
	if _, ok := item["qr"]; ok {
		t.Fatalf("expired QR should be cleared by whole-item upsert: %v", msg)
	}

	server.daemon.SetStateDetail(app.StateOnline, "connected")
	msg = c.expectUpsert(sub, "self")
	item = msg["item"].(map[string]any)
	if item["state"] != "online" || item["detail"] != "connected" {
		t.Fatalf("online login item = %v", msg)
	}
}

func TestSyncViewInitialAndLiveProgress(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:               app.HistorySyncTypeRecent,
		ProgressPercent:        42,
		ChunkOrder:             7,
		ConversationsInChunk:   3,
		MessagesInChunk:        99,
		Phase:                  app.HistorySyncPhaseProcessing,
		ProcessedConversations: 2,
		ProcessedMessages:      50,
	})
	RegisterDaemonViews(server, server.daemon, nil, nil)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"sync"}`)
	msg := c.expectUpsert(sub, "self")
	item := msg["item"].(map[string]any)
	if item["type"] != "recent" || item["phase"] != "processing" || item["progress_percent"] != float64(42) || item["chunk_order"] != float64(7) || item["messages_in_chunk"] != float64(99) {
		t.Fatalf("initial sync item = %v", msg)
	}
	c.expectReady(sub, true)

	server.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:        app.HistorySyncTypeRecent,
		ProgressPercent: 100,
		IsComplete:      true,
		Phase:           app.HistorySyncPhaseComplete,
	})
	msg = c.expectUpsert(sub, "self")
	item = msg["item"].(map[string]any)
	if item["phase"] != "complete" || item["is_complete"] != true || item["progress_percent"] != float64(100) {
		t.Fatalf("complete sync item = %v", msg)
	}
}
