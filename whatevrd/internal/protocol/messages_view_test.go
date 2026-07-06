package protocol

import (
	"context"
	"fmt"
	"testing"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// seedTextMessage inserts one text message into the chat and returns its id.
func seedTextMessage(t *testing.T, db *store.DB, chatID, text string, ts time.Time) string {
	t.Helper()
	id := fmt.Sprintf("m-%s-%d", chatID, ts.UnixNano())
	if _, err := db.SaveTextMessage(context.Background(), store.TextMessageInput{
		ID:        id,
		ChatID:    chatID,
		Text:      text,
		Timestamp: ts,
		Direction: store.DirectionIncoming,
	}); err != nil {
		t.Fatalf("seed message %q: %v", text, err)
	}
	return id
}

// extend grows a subscription's window and reads the empty result.
func (c *testClient) extend(reqID int, sub float64, count int) {
	c.t.Helper()
	c.sendLine(fmt.Sprintf(`{"id":%d,"method":"extend","params":{"sub":%d,"count":%d}}`, reqID, int64(sub), count))
	if _, ok := c.recv()["result"]; !ok {
		c.t.Fatalf("extend %d failed", reqID)
	}
}

func TestMessagesViewInitialLatestWindow(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	seedTextMessage(t, db, chat, "one", base)
	seedTextMessage(t, db, chat, "two", base.Add(time.Minute))
	seedTextMessage(t, db, chat, "three", base.Add(2*time.Minute))

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))

	// The window is anchored at the live edge, so the fill arrives newest-first
	// while each item's sort key ascends with time (client renders oldest→newest).
	prev := ""
	for i, text := range []string{"three", "two", "one"} {
		msg := c.recvEvent()
		if msg["event"] != "upsert" || msg["sub"] != sub {
			t.Fatalf("expected upsert, got %v", msg)
		}
		item := msg["item"].(map[string]any)
		if item["fallback"] != text {
			t.Fatalf("fill[%d] fallback = %v, want %q", i, item["fallback"], text)
		}
		if item["kind"] != "text" {
			t.Fatalf("fill[%d] kind = %v, want text", i, item["kind"])
		}
		sortKey := msg["sort"].(string)
		if prev != "" && sortKey >= prev {
			t.Fatalf("newest-first fill should descend in sort key: %q then %q", prev, sortKey)
		}
		prev = sortKey
	}
	c.expectReady(sub, true)
}

func TestMessagesViewLiveEdgeAndWindowFallOut(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	seedTextMessage(t, db, chat, "old", base)
	seedTextMessage(t, db, chat, "new", base.Add(time.Minute))

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q,"limit":1}`, chat))
	first := c.recvEvent() // newest message ("new")
	if first["item"].(map[string]any)["fallback"] != "new" {
		t.Fatalf("window head = %v, want new", first["item"])
	}
	c.expectReady(sub, false) // "old" remains beyond the size-1 window

	// A newer message arrives at the live edge: it enters the window and the
	// previous head falls out.
	newest := seedTextMessage(t, db, chat, "newest", base.Add(2*time.Minute))
	daemon.PublishNewMessage(app.Message{ID: newest, ChatID: chat}, appChatFor(t, db, chat))
	up := c.recvEvent()
	if up["event"] != "upsert" || up["item"].(map[string]any)["fallback"] != "newest" {
		t.Fatalf("expected upsert of newest, got %v", up)
	}
	c.expectRemove(sub, seedID(chat, base.Add(time.Minute)))
	_ = ctx
}

func TestMessagesViewRevokeAsUpsert(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	id := seedTextMessage(t, db, chat, "secret", base)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	c.expectUpsert(sub, id)
	c.expectReady(sub, true)

	if _, _, _, err := db.MarkMessageRevoked(ctx, id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	daemon.PublishMessageUpdated(app.Message{ID: id, ChatID: chat, IsRevoked: true})

	msg := c.expectUpsert(sub, id)
	item := msg["item"].(map[string]any)
	if item["revoked"] != true {
		t.Fatalf("revoked message not flagged: %v", item)
	}
	if item["fallback"] != "This message was deleted" {
		t.Fatalf("revoked fallback = %v", item["fallback"])
	}
	if _, hasText := item["text"]; hasText {
		t.Fatalf("revoked message still carries text: %v", item)
	}
}

func TestMessagesViewDeleteAsRemove(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	id := seedTextMessage(t, db, chat, "byebye", base)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	c.expectUpsert(sub, id)
	c.expectReady(sub, true)

	if _, _, _, err := db.DeleteMessageForMe(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	daemon.PublishMessageDeleted(chat, id, appChatFor(t, db, chat))
	c.expectRemove(sub, id)
}

func TestMessagesViewExtendReachesOlder(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	ids := make([]string, 3)
	for i := range ids {
		ids[i] = seedTextMessage(t, db, chat, fmt.Sprintf("msg%d", i), base.Add(time.Duration(i)*time.Minute))
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q,"limit":1}`, chat))
	c.expectUpsert(sub, ids[2]) // newest only
	c.expectReady(sub, false)   // older remain

	c.extend(3, sub, 2)
	// Extending the window reaches the two older messages; the newest is
	// already held, so only the older two upsert, then ready reports the whole
	// chat is now local.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		msg := c.recvEvent()
		if msg["event"] != "upsert" {
			t.Fatalf("expected upsert during extend, got %v", msg)
		}
		got[msg["item"].(map[string]any)["id"].(string)] = true
	}
	if !got[ids[0]] || !got[ids[1]] {
		t.Fatalf("extend did not deliver both older messages: %v", got)
	}
	c.expectReady(sub, true)
}

func TestMessagesViewImageItemShape(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	id := "img-1"
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:        id,
			ChatID:    chat,
			Text:      "a caption",
			Timestamp: base,
			Direction: store.DirectionIncoming,
		},
		MediaKind:               store.MediaKindImage,
		MediaMimeType:           "image/jpeg",
		MediaWidth:              640,
		MediaHeight:             480,
		MediaThumbnailLocalPath: "/cache/thumb.jpg",
		MediaLocalPath:          "/cache/full.jpg",
	}); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, id)["item"].(map[string]any)
	if item["kind"] != "image" {
		t.Fatalf("kind = %v, want image", item["kind"])
	}
	if item["fallback"] != "a caption" {
		t.Fatalf("fallback = %v, want caption", item["fallback"])
	}
	media, ok := item["media"].(map[string]any)
	if !ok {
		t.Fatalf("image item missing media: %v", item)
	}
	if media["mime"] != "image/jpeg" || media["path"] != "/cache/full.jpg" || media["thumbnail_path"] != "/cache/thumb.jpg" {
		t.Fatalf("media fields wrong: %v", media)
	}
	if media["width"] != float64(640) || media["height"] != float64(480) {
		t.Fatalf("media dimensions wrong: %v", media)
	}
	sender := item["sender"].(map[string]any)
	if sender["id"] != chat {
		t.Fatalf("sender.id = %v, want %v", sender["id"], chat)
	}
	c.expectReady(sub, true)
}

func TestMessagesViewParamErrors(t *testing.T) {
	socketPath, _, _ := startChatsTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()

	for _, params := range []string{
		`{"view":"messages"}`,                                  // no chat_id
		`{"view":"messages","chat_id":"c@s.whatsapp.net","anchor":"unread"}`,
		`{"view":"messages","chat_id":"c@s.whatsapp.net","anchor":"3EB0abc"}`,
	} {
		c.sendLine(`{"id":9,"method":"subscribe","params":` + params + `}`)
		if code := errorCode(t, c.recv()); code != CodeInvalidParams {
			t.Errorf("subscribe %s: code = %q, want %q", params, code, CodeInvalidParams)
		}
	}
}

// seedID recomputes the id seedTextMessage assigns for a chat+timestamp.
func seedID(chatID string, ts time.Time) string {
	return fmt.Sprintf("m-%s-%d", chatID, ts.UnixNano())
}

// appChatFor loads a chat and projects it for a daemon event payload.
func appChatFor(t *testing.T, db *store.DB, chatID string) app.Chat {
	t.Helper()
	chat, err := db.GetChat(context.Background(), chatID)
	if err != nil {
		t.Fatalf("get chat %q: %v", chatID, err)
	}
	return toTestAppChat(chat)
}
