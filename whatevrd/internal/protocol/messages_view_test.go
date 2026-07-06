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

// extend grows a subscription's window toward direction and reads the empty
// result.
func (c *testClient) extend(reqID int, sub float64, count int, direction string) {
	c.t.Helper()
	c.sendLine(fmt.Sprintf(`{"id":%d,"method":"extend","params":{"sub":%d,"count":%d,"direction":%q}}`, reqID, int64(sub), count, direction))
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

	c.extend(3, sub, 2, "older")
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
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	seedTextMessage(t, db, chat, "hi", time.Unix(1_700_000_000, 0))
	c := dialTest(t, socketPath)
	c.hello()

	cases := []struct {
		params string
		want   string
	}{
		{`{"view":"messages"}`, CodeInvalidParams}, // no chat_id
		// A message-id anchor naming a message that is not in this chat is a
		// not_found, not a params error.
		{fmt.Sprintf(`{"view":"messages","chat_id":%q,"anchor":"3EB0missing"}`, chat), CodeNotFound},
	}
	for _, tc := range cases {
		c.sendLine(`{"id":9,"method":"subscribe","params":` + tc.params + `}`)
		if code := errorCode(t, c.recv()); code != tc.want {
			t.Errorf("subscribe %s: code = %q, want %q", tc.params, code, tc.want)
		}
	}
}

// subscribeResult issues a subscribe and returns the full result object (so a
// caller can read subscribe meta such as anchor_id), plus the sub id.
func (c *testClient) subscribeResult(reqID int, params string) (float64, map[string]any) {
	c.t.Helper()
	c.sendLine(fmt.Sprintf(`{"id":%d,"method":"subscribe","params":%s}`, reqID, params))
	result, ok := c.recv()["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("subscribe %s failed", params)
	}
	sub, ok := result["sub"].(float64)
	if !ok {
		c.t.Fatalf("subscribe result has no sub: %v", result)
	}
	return sub, result
}

// collectUpserts reads exactly n upserts and returns id→sort so a test can
// assert set membership and render (sort-key) order without depending on the
// engine's proximity emit order.
func (c *testClient) collectUpserts(sub float64, n int) map[string]string {
	c.t.Helper()
	got := map[string]string{}
	for i := 0; i < n; i++ {
		msg := c.recvEvent()
		if msg["event"] != "upsert" || msg["sub"] != sub {
			c.t.Fatalf("expected upsert %d/%d, got %v", i+1, n, msg)
		}
		got[msg["item"].(map[string]any)["id"].(string)] = msg["sort"].(string)
	}
	return got
}

func TestMessagesViewUnreadAnchor(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	ids := make([]string, 6) // m0 (oldest) .. m5 (newest), all incoming
	for i := range ids {
		ids[i] = seedTextMessage(t, db, chat, fmt.Sprintf("m%d", i), base.Add(time.Duration(i)*time.Minute))
	}
	// Two unread: the anchor is the oldest unread incoming message, i.e. m4.
	if _, _, err := db.OverwriteChatUnreadCount(context.Background(), chat, 2); err != nil {
		t.Fatalf("set unread: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub, result := c.subscribeResult(2, fmt.Sprintf(`{"view":"messages","chat_id":%q,"anchor":"unread","limit":3}`, chat))
	if result["anchor_id"] != ids[4] {
		t.Fatalf("anchor_id = %v, want %v", result["anchor_id"], ids[4])
	}

	// A size-3 window balances around m4: one older (m3), the anchor, one newer (m5).
	win := c.collectUpserts(sub, 3)
	for _, want := range []string{ids[3], ids[4], ids[5]} {
		if _, ok := win[want]; !ok {
			t.Fatalf("window missing %s: %v", want, win)
		}
	}
	if !(win[ids[3]] < win[ids[4]] && win[ids[4]] < win[ids[5]]) {
		t.Fatalf("sort keys not oldest→newest: %v", win)
	}
	c.expectReady(sub, false) // m0..m2 remain older
}

func TestMessagesViewMessageIDAnchorDirectionalExtend(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	ids := make([]string, 5) // m0..m4
	for i := range ids {
		ids[i] = seedTextMessage(t, db, chat, fmt.Sprintf("m%d", i), base.Add(time.Duration(i)*time.Minute))
	}

	c := dialTest(t, socketPath)
	c.hello()
	// Anchor on the middle message m2 with a size-3 window: m1, m2, m3.
	sub, result := c.subscribeResult(2, fmt.Sprintf(`{"view":"messages","chat_id":%q,"anchor":%q,"limit":3}`, chat, ids[2]))
	if result["anchor_id"] != ids[2] {
		t.Fatalf("anchor_id = %v, want %v", result["anchor_id"], ids[2])
	}
	win := c.collectUpserts(sub, 3)
	for _, want := range []string{ids[1], ids[2], ids[3]} {
		if _, ok := win[want]; !ok {
			t.Fatalf("window missing %s: %v", want, win)
		}
	}
	c.expectReady(sub, false) // m0 and m4 remain, one on each side

	// Extend OLDER only: the older m0 arrives (the newer m4 does not), and the
	// older frontier is now exhausted.
	c.extend(3, sub, 1, "older")
	older := c.collectUpserts(sub, 1)
	if _, ok := older[ids[0]]; !ok {
		t.Fatalf("extend older did not reach m0: %v", older)
	}
	c.expectReady(sub, true) // older frontier exhausted

	// Extend NEWER only: now the newer m4 arrives and the newer frontier is
	// exhausted too.
	c.extend(4, sub, 1, "newer")
	newer := c.collectUpserts(sub, 1)
	if _, ok := newer[ids[4]]; !ok {
		t.Fatalf("extend newer did not reach m4: %v", newer)
	}
	c.expectReady(sub, true) // newer frontier exhausted
}

func TestMessagesViewExtendNewerOnLatestErrors(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	older := seedTextMessage(t, db, chat, "older", base)
	seedTextMessage(t, db, chat, "newer", base.Add(time.Minute))

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q,"limit":1}`, chat))
	c.expectUpsert(sub, seedID(chat, base.Add(time.Minute))) // newest
	c.expectReady(sub, false)                                // older remains

	// `newer` is meaningless on a live-edge window: the newer edge is the live
	// edge, where messages arrive unsolicited.
	c.sendLine(fmt.Sprintf(`{"id":3,"method":"extend","params":{"sub":%d,"count":1,"direction":"newer"}}`, int64(sub)))
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("extend newer on latest: code = %q, want %q", code, CodeInvalidParams)
	}

	// `older` still reaches back into history.
	c.extend(4, sub, 1, "older")
	c.expectUpsert(sub, older)
	c.expectReady(sub, true)
}

func TestMessagesViewUnreadAnchorNoneDegradesToLiveEdge(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	newest := seedTextMessage(t, db, chat, "hello", base)

	c := dialTest(t, socketPath)
	c.hello()
	// unread_count is 0, so the unread anchor degrades to the live edge: no
	// anchor_id in the meta, and the newest message fills the window.
	sub, result := c.subscribeResult(2, fmt.Sprintf(`{"view":"messages","chat_id":%q,"anchor":"unread"}`, chat))
	if _, has := result["anchor_id"]; has {
		t.Fatalf("expected no anchor_id when nothing is unread: %v", result)
	}
	c.expectUpsert(sub, newest)
	c.expectReady(sub, true)
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
