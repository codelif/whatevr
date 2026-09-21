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

	if _, _, _, err := db.MarkMessageRevoked(ctx, id, false); err != nil {
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

// A group invite crosses as its own nested object carrying both readings of
// the group: what the sender's client claimed and what the daemon resolved.
// The card renders from whichever it has, so both have to survive the wire, and
// `fallback` has to name the group for a frontend that renders neither.
func TestMessagesViewGroupInviteItemShape(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	id := "inv-1"
	payload, err := store.EncodePayload(store.MessagePayload{GroupInvite: &store.GroupInvitePayload{
		GroupJID:    "120363000000000001@g.us",
		Code:        "CODE123",
		ExpiresAt:   1_700_100_000,
		Name:        "sender's copy",
		Subject:     "Wow3",
		MemberCount: 12,
		ResolvedAt:  1_700_000_100,
		PhotoPath:   "/cache/invite.jpg",
	}})
	if err != nil {
		t.Fatalf("encode invite: %v", err)
	}
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:          id,
			ChatID:      chat,
			Timestamp:   time.Unix(1_700_000_000, 0),
			Direction:   store.DirectionIncoming,
			PayloadJSON: payload,
		},
		MediaKind:      store.MediaKindGroupInvite,
		PayloadSummary: "Wow3",
	}); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, id)["item"].(map[string]any)
	if item["kind"] != "group_invite" {
		t.Fatalf("kind = %v, want group_invite", item["kind"])
	}
	if item["fallback"] != "👥 Group invite: Wow3" {
		t.Fatalf("fallback = %v", item["fallback"])
	}
	// An invite has nothing to fetch. A `media` object here would make every
	// part of the frontend that asks "is there anything to download" say yes.
	if _, ok := item["media"]; ok {
		t.Fatalf("group invite carried a media object: %v", item)
	}
	invite, ok := item["invite"].(map[string]any)
	if !ok {
		t.Fatalf("group invite item missing its payload: %v", item)
	}
	if invite["group_jid"] != "120363000000000001@g.us" || invite["code"] != "CODE123" {
		t.Fatalf("invite identity wrong: %v", invite)
	}
	if invite["subject"] != "Wow3" || invite["name"] != "sender's copy" {
		t.Fatalf("both readings of the name must survive: %v", invite)
	}
	if invite["member_count"] != float64(12) || invite["expires_at"] != float64(1_700_100_000) {
		t.Fatalf("resolved facts wrong: %v", invite)
	}
	if invite["photo_path"] != "/cache/invite.jpg" {
		t.Fatalf("photo path wrong: %v", invite)
	}
	// Nothing resolved membership, so the card must not be told we are in.
	if _, ok := invite["joined"]; ok {
		t.Fatalf("an unresolved invite claimed membership: %v", invite)
	}
	c.expectReady(sub, true)
}

// A link preview is the one payload that does not stand for its message. The
// row's kind stays `text` and its fallback stays the words somebody typed, so
// everything that reads a message by its kind carries on working and a
// frontend that has never heard of a preview renders the message unchanged.
func TestMessagesViewLinkPreviewRidesATextRow(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	id := "lp-1"
	payload, err := store.EncodePayload(store.MessagePayload{LinkPreview: &store.LinkPreviewPayload{
		URL:             "https://example.com/road",
		Host:            "example.com",
		Title:           "The road",
		Description:     "A page about a road.",
		Type:            "image",
		ThumbnailPath:   "/cache/lp-1.link.jpg",
		ThumbnailWidth:  200,
		ThumbnailHeight: 112,
	}})
	if err != nil {
		t.Fatalf("encode preview: %v", err)
	}
	if _, err := db.SaveTextMessage(context.Background(), store.TextMessageInput{
		ID:          id,
		ChatID:      chat,
		Text:        "look at this https://example.com/road",
		Timestamp:   time.Unix(1_700_000_000, 0),
		Direction:   store.DirectionIncoming,
		PayloadJSON: payload,
	}); err != nil {
		t.Fatalf("seed preview: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, id)["item"].(map[string]any)
	if item["kind"] != "text" {
		t.Fatalf("kind = %v, a message with a preview is still a text message", item["kind"])
	}
	if item["text"] != "look at this https://example.com/road" {
		t.Fatalf("text = %v", item["text"])
	}
	if item["fallback"] != "look at this https://example.com/road" {
		t.Fatalf("fallback = %v, the preview must not have taken over the one-line rendering", item["fallback"])
	}
	// The thumbnail is a file the daemon already wrote. A `media` object would
	// make every part of a frontend that asks "is there anything to fetch"
	// say yes about a card that is already complete.
	if _, ok := item["media"]; ok {
		t.Fatalf("link preview carried a media object: %v", item)
	}
	preview, ok := item["link_preview"].(map[string]any)
	if !ok {
		t.Fatalf("text row missing its link preview: %v", item)
	}
	if preview["url"] != "https://example.com/road" || preview["host"] != "example.com" {
		t.Fatalf("link identity wrong: %v", preview)
	}
	if preview["title"] != "The road" || preview["type"] != "image" {
		t.Fatalf("card contents wrong: %v", preview)
	}
	if preview["thumbnail_path"] != "/cache/lp-1.link.jpg" {
		t.Fatalf("thumbnail path wrong: %v", preview)
	}
	if preview["thumb_width"] != float64(200) || preview["thumb_height"] != float64(112) {
		t.Fatalf("thumbnail shape wrong: %v", preview)
	}
	c.expectReady(sub, true)
}

// An album is one row on the wire carrying whole message items for its
// pictures. A frontend never sees the children as rows of their own and never
// merges anything (rule 3), and a tile is a message item in every respect, so
// it renders with the code a lone photo already has.
func TestMessagesViewAlbumCarriesItsPicturesAsItems(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	ctx := context.Background()
	payload, err := store.EncodePayload(store.MessagePayload{Album: &store.AlbumPayload{ExpectedImages: 3}})
	if err != nil {
		t.Fatalf("encode album: %v", err)
	}
	if _, err := db.SaveMediaMessage(ctx, store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:          "al-1",
			ChatID:      chat,
			Timestamp:   time.Unix(1_700_000_000, 0),
			Direction:   store.DirectionIncoming,
			PayloadJSON: payload,
		},
		MediaKind:      store.MediaKindAlbum,
		PayloadSummary: "3 photos",
	}); err != nil {
		t.Fatalf("seed album: %v", err)
	}
	for i, id := range []string{"al-1-p1", "al-1-p2"} {
		if _, err := db.SaveMediaMessage(ctx, store.MediaMessageInput{
			TextMessageInput: store.TextMessageInput{
				ID:        id,
				ChatID:    chat,
				Timestamp: time.Unix(1_700_000_001, 0),
				Direction: store.DirectionIncoming,
			},
			MediaKind:               store.MediaKindImage,
			MediaMimeType:           "image/jpeg",
			MediaThumbnailLocalPath: "/cache/" + id + ".thumb.jpg",
			AlbumParentID:           "al-1",
			AlbumIndex:              int32(i),
		}); err != nil {
			t.Fatalf("seed picture %s: %v", id, err)
		}
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, "al-1")["item"].(map[string]any)
	c.expectReady(sub, true)

	if item["kind"] != "album" {
		t.Fatalf("kind = %v, want album", item["kind"])
	}
	// The header has nothing to fetch; its pictures do.
	if _, ok := item["media"]; ok {
		t.Fatalf("album header carried a media object: %v", item)
	}
	album, ok := item["album"].(map[string]any)
	if !ok {
		t.Fatalf("album item missing its payload: %v", item)
	}
	items, ok := album["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("album items = %v", album["items"])
	}
	// One picture is still missing, and an album that is still filling says so.
	if album["expected"] != float64(3) {
		t.Fatalf("expected = %v, want 3 while the album is short", album["expected"])
	}
	first := items[0].(map[string]any)
	if first["id"] != "al-1-p1" || first["kind"] != "image" {
		t.Fatalf("first tile is not a whole image item: %v", first)
	}
	if first["media"].(map[string]any)["thumbnail_path"] != "/cache/al-1-p1.thumb.jpg" {
		t.Fatalf("a tile lost its media object: %v", first)
	}

	// A tile is a message with a fetch of its own, so its progress ring is its
	// own: one tile downloading must not light up the rest.
	daemon.PublishMediaDownloadChanged("al-1-p2", chat, true, "", 0, 1024)
	album = c.expectUpsert(sub, "al-1")["item"].(map[string]any)["album"].(map[string]any)
	items = album["items"].([]any)
	if items[0].(map[string]any)["media"].(map[string]any)["downloading"] != nil {
		t.Fatalf("an idle tile reports downloading: %v", items[0])
	}
	if items[1].(map[string]any)["media"].(map[string]any)["downloading"] != true {
		t.Fatalf("the downloading tile does not say so: %v", items[1])
	}
}

// Whether a fetch is in flight rides the message row, so a renderer never has
// to join it against `transfers` and never sees the two disagree: the terminal
// update both clears `downloading` and delivers the path.
func TestMessagesViewCarriesDownloadingOnTheRow(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	id := "img-dl"
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:        id,
			ChatID:    chat,
			Timestamp: base,
			Direction: store.DirectionIncoming,
		},
		MediaKind:     store.MediaKindImage,
		MediaMimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	mediaOf := func(msg map[string]any) map[string]any {
		item := msg["item"].(map[string]any)
		media, ok := item["media"].(map[string]any)
		if !ok {
			t.Fatalf("item carries no media: %v", item)
		}
		return media
	}
	if media := mediaOf(c.expectUpsert(sub, id)); media["downloading"] != nil {
		t.Fatalf("idle row reports downloading: %v", media)
	}
	c.expectReady(sub, true)

	daemon.PublishMediaDownloadChanged(id, chat, true, "", 0, 1024)
	if media := mediaOf(c.expectUpsert(sub, id)); media["downloading"] != true {
		t.Fatalf("started download not on the row: %v", media)
	}

	// Progress does not move the bool, so it must not re-emit the row: the byte
	// counters are `transfers`' job. If it did, the next frame read below would
	// be a duplicate rather than the terminal update.
	daemon.PublishMediaDownloadChanged(id, chat, true, "", 256, 1024)
	daemon.PublishMediaDownloadChanged(id, chat, true, "", 512, 1024)

	if _, err := db.UpdateMessageMediaLocalPathWithDimensions(context.Background(), id, "/cache/full.jpg", 0, 0); err != nil {
		t.Fatalf("store downloaded path: %v", err)
	}
	daemon.PublishMediaDownloadChanged(id, chat, false, "", 1024, 1024)

	media := mediaOf(c.expectUpsert(sub, id))
	if media["downloading"] != nil {
		t.Fatalf("finished download still reports downloading: %v", media)
	}
	if media["path"] != "/cache/full.jpg" {
		t.Fatalf("finished download did not deliver the path: %v", media)
	}
}

// noteDownloadChanged is the filter that keeps progress ticks from re-reading
// the whole window: only the two edges of a transfer count as a change.
func TestMessagesFeedInvalidatesOnlyOnDownloadEdges(t *testing.T) {
	f := &messagesChatFeed{chatID: "c@s.whatsapp.net"}
	start := app.MediaDownloadEvent{MessageID: "m1", ChatID: f.chatID, Downloading: true}
	if !f.noteDownloadChanged(start) {
		t.Fatal("first downloading event did not count as a change")
	}
	if !f.isDownloading("m1") {
		t.Fatal("feed did not record the transfer")
	}
	progress := start
	progress.ReceivedBytes = 512
	if f.noteDownloadChanged(progress) {
		t.Fatal("a progress tick invalidated the window")
	}
	done := app.MediaDownloadEvent{MessageID: "m1", ChatID: f.chatID}
	if !f.noteDownloadChanged(done) {
		t.Fatal("terminal event did not count as a change")
	}
	if f.isDownloading("m1") {
		t.Fatal("feed still reports a finished transfer")
	}
	if f.noteDownloadChanged(done) {
		t.Fatal("a repeated terminal event invalidated the window")
	}
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

// An anchored window is bounded until its newer frontier reaches the present;
// after that it stays adjacent to the live edge and keeps growing there
// (PROTOCOL.md, "Windows"). Both halves are asserted here: a message past the
// frontier is withheld (delivering it would leave a render gap), while one
// arriving after the frontier has reached the live edge is contiguous with the
// window and lands as an ordinary upsert.
func TestMessagesViewAnchoredWindowFollowsTheLiveEdgeOnceReached(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	ids := make([]string, 4) // m0..m3
	for i := range ids {
		ids[i] = seedTextMessage(t, db, chat, fmt.Sprintf("m%d", i), base.Add(time.Duration(i)*time.Minute))
	}

	c := dialTest(t, socketPath)
	c.hello()
	// Anchor on m1 with a size-3 window: m0, m1, m2. m3 is one past the newer
	// frontier.
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q,"anchor":%q,"limit":3}`, chat, ids[1]))
	c.collectUpserts(sub, 3)
	c.expectReady(sub, false)

	// A message arrives at the present while the frontier is still short of it.
	// m3 sits between the window and m4, so m4 is not contiguous and must be
	// withheld, so the next event is whatever the following extend produces.
	m4 := seedTextMessage(t, db, chat, "m4", base.Add(4*time.Minute))
	daemon.PublishNewMessage(app.Message{ID: m4, ChatID: chat}, appChatFor(t, db, chat))

	c.extend(3, sub, 1, "newer")
	c.expectUpsert(sub, ids[3]) // m3, not m4
	c.expectReady(sub, false)   // m4 is still beyond the frontier

	// One more extend closes the gap: the frontier now holds the newest message
	// in the chat, so it reports exhausted and latches onto the live edge.
	c.extend(4, sub, 1, "newer")
	c.expectUpsert(sub, m4)
	c.expectReady(sub, true)

	// From here the window follows the present: no extend, no re-subscribe.
	m5 := seedTextMessage(t, db, chat, "m5", base.Add(5*time.Minute))
	daemon.PublishNewMessage(app.Message{ID: m5, ChatID: chat}, appChatFor(t, db, chat))
	c.expectUpsert(sub, m5)
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

// An inbound view-once row keeps its real kind and keys in the store but must
// render as the unsupported tombstone on the wire, with the view_once flag
// set so the frontend can offer an explicit save.
func TestMessagesViewInboundViewOnceRendersTombstone(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	id := "vo-1"
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:        id,
			ChatID:    chat,
			Text:      "View once photo",
			Timestamp: time.Unix(1_700_000_000, 0),
			Direction: store.DirectionIncoming,
		},
		MediaKind:     store.MediaKindImage,
		MediaMimeType: "image/jpeg",
		MediaPayload:  []byte{0x0a, 0x01, 0x61},
		IsViewOnce:    true,
	}); err != nil {
		t.Fatalf("seed view-once: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, id)["item"].(map[string]any)
	if item["kind"] != "unsupported" {
		t.Fatalf("kind = %v, want unsupported", item["kind"])
	}
	if item["fallback"] != "View once photo" {
		t.Fatalf("fallback = %v, want the tombstone label", item["fallback"])
	}
	if item["view_once"] != true {
		t.Fatalf("view_once = %v, want true", item["view_once"])
	}
	c.expectReady(sub, true)
}

func TestMessagesViewLinkPreviewItemShape(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	payload, err := store.EncodePayload(store.MessagePayload{LinkPreview: &store.LinkPreviewPayload{
		URL: "https://example.com/a", Title: "Example", Description: "An example page", ThumbnailPath: "/cache/linkpreview.jpg",
	}})
	if err != nil {
		t.Fatalf("encode preview: %v", err)
	}
	if _, err := db.SaveTextMessage(context.Background(), store.TextMessageInput{
		ID:          "link-1",
		ChatID:      chat,
		Text:        "check https://example.com/a",
		Timestamp:   time.Unix(1_700_000_000, 0),
		Direction:   store.DirectionIncoming,
		PayloadJSON: payload,
	}); err != nil {
		t.Fatalf("seed link message: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, "link-1")["item"].(map[string]any)
	preview, ok := item["link_preview"].(map[string]any)
	if !ok {
		t.Fatalf("link item missing link_preview: %v", item)
	}
	if preview["url"] != "https://example.com/a" || preview["title"] != "Example" || preview["description"] != "An example page" || preview["thumbnail_path"] != "/cache/linkpreview.jpg" {
		t.Fatalf("link preview fields wrong: %v", preview)
	}
	c.expectReady(sub, true)
}

func TestMessagesViewPollContactLocationShapes(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	base := time.Unix(1_700_000_000, 0)
	pollPayload, err := store.EncodePayload(store.MessagePayload{Poll: &store.PollPayload{
		Question: "dinner?", SelectableCount: 1,
	}})
	if err != nil {
		t.Fatalf("encode poll: %v", err)
	}
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:          "poll-1",
			ChatID:      chat,
			Timestamp:   base,
			Direction:   store.DirectionIncoming,
			PayloadJSON: pollPayload,
		},
		MediaKind:      store.MediaKindPoll,
		PayloadSummary: "dinner?",
	}); err != nil {
		t.Fatalf("seed poll: %v", err)
	}
	if err := db.SavePollOptions(context.Background(), "poll-1", []store.PollOption{
		{Index: 0, Name: "yes", SHA256: []byte("hash-yes")},
		{Index: 1, Name: "no", SHA256: []byte("hash-no")},
	}); err != nil {
		t.Fatalf("seed poll options: %v", err)
	}
	contactPayload, err := store.EncodePayload(store.MessagePayload{Contacts: &store.ContactsPayload{
		Cards: []store.ContactCard{{
			DisplayName: "Bob",
			Phones:      []store.ContactField{{Label: "CELL", Value: "+1234"}},
		}},
	}})
	if err != nil {
		t.Fatalf("encode contact: %v", err)
	}
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:          "contact-1",
			ChatID:      chat,
			Text:        "Bob",
			Timestamp:   base.Add(time.Second),
			Direction:   store.DirectionIncoming,
			PayloadJSON: contactPayload,
		},
		MediaKind:      store.MediaKindContact,
		PayloadSummary: "Bob",
	}); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	locationPayload, err := store.EncodePayload(store.MessagePayload{Location: &store.LocationPayload{
		Latitude: 1.5, Longitude: 2.5, Name: "Here",
	}})
	if err != nil {
		t.Fatalf("encode location: %v", err)
	}
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:          "loc-1",
			ChatID:      chat,
			Text:        "Here",
			Timestamp:   base.Add(2 * time.Second),
			Direction:   store.DirectionIncoming,
			PayloadJSON: locationPayload,
		},
		MediaKind:      store.MediaKindLocation,
		PayloadSummary: "Here",
	}); err != nil {
		t.Fatalf("seed location: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	items := map[string]map[string]any{}
	for _, id := range []string{"poll-1", "contact-1", "loc-1"} {
		msg := c.recvEvent()
		item, ok := msg["item"].(map[string]any)
		if !ok {
			t.Fatalf("upsert without an item: %v", msg)
		}
		items[item["id"].(string)] = item
		_ = id
	}
	pollItem := items["poll-1"]
	poll, ok := pollItem["poll"].(map[string]any)
	if !ok || poll["question"] != "dinner?" {
		t.Fatalf("poll item wrong: %v", pollItem)
	}
	options, ok := poll["options"].([]any)
	if !ok || len(options) != 2 || options[0].(map[string]any)["name"] != "yes" {
		t.Fatalf("poll options wrong: %v", poll)
	}
	contactItem := items["contact-1"]
	contacts, ok := contactItem["contacts"].(map[string]any)
	if !ok {
		t.Fatalf("contact item wrong: %v", contactItem)
	}
	cards, ok := contacts["cards"].([]any)
	if !ok || len(cards) != 1 {
		t.Fatalf("contact cards wrong: %v", contacts)
	}
	card := cards[0].(map[string]any)
	if card["display_name"] != "Bob" {
		t.Fatalf("contact card wrong: %v", card)
	}
	phones, ok := card["phones"].([]any)
	if !ok || len(phones) != 1 || phones[0].(map[string]any)["value"] != "+1234" {
		t.Fatalf("contact phones wrong: %v", card)
	}
	locItem := items["loc-1"]
	loc, ok := locItem["location"].(map[string]any)
	if !ok || loc["lat"] != float64(1.5) || loc["lng"] != float64(2.5) || loc["name"] != "Here" {
		t.Fatalf("location item wrong: %v", locItem)
	}
	c.expectReady(sub, true)
}

// A business message crosses as one card whatever wire shape it arrived in, and
// carries no `media` object: nothing on it is downloadable, and a kind that
// looks fetchable puts marketing broadcasts on the auto-download path.
func TestMessagesViewInteractiveItemShape(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	id := "biz-1"
	payload, err := store.EncodePayload(store.MessagePayload{Interactive: &store.InteractivePayload{
		Source:        "template",
		Title:         "Your parcel is out for delivery",
		Body:          "BLR-4471 left the hub at 08:12.",
		Footer:        "Sent by Bluedart",
		ThumbnailPath: "/cache/biz-1.card.jpg",
		Buttons: []store.InteractiveButton{
			{Kind: store.InteractiveButtonURL, Label: "Track parcel", URL: "https://example.com/t", Live: true},
			{Kind: store.InteractiveButtonReply, Label: "Leave with a neighbour", ID: "n"},
		},
		Sections: []store.InteractiveSection{{
			Title: "Options",
			Rows:  []store.InteractiveRow{{Title: "Reschedule", Description: "Pick another day", ID: "r"}},
		}},
	}})
	if err != nil {
		t.Fatalf("encode interactive: %v", err)
	}
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:          id,
			ChatID:      chat,
			Timestamp:   time.Unix(1_700_000_000, 0),
			Direction:   store.DirectionIncoming,
			PayloadJSON: payload,
		},
		MediaKind:      store.MediaKindInteractive,
		PayloadSummary: "BLR-4471 left the hub at 08:12.",
	}); err != nil {
		t.Fatalf("seed interactive: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, id)["item"].(map[string]any)
	if item["kind"] != "interactive" {
		t.Fatalf("kind = %v", item["kind"])
	}
	// The one-line rendering is what the message says. "Message" with the
	// message hidden behind it is what this used to be.
	if item["fallback"] != "💬 BLR-4471 left the hub at 08:12." {
		t.Fatalf("fallback = %v", item["fallback"])
	}
	if _, ok := item["media"]; ok {
		t.Fatalf("a business message carried a media object: %v", item)
	}
	interactive, ok := item["interactive"].(map[string]any)
	if !ok {
		t.Fatalf("interactive row missing its card: %v", item)
	}
	if interactive["title"] != "Your parcel is out for delivery" || interactive["footer"] != "Sent by Bluedart" {
		t.Fatalf("card contents wrong: %v", interactive)
	}
	if interactive["thumbnail_path"] != "/cache/biz-1.card.jpg" {
		t.Fatalf("thumbnail path wrong: %v", interactive)
	}

	buttons, ok := interactive["buttons"].([]any)
	if !ok || len(buttons) != 2 {
		t.Fatalf("buttons = %v", interactive["buttons"])
	}
	live := buttons[0].(map[string]any)
	if live["kind"] != "url" || live["live"] != true || live["url"] != "https://example.com/t" {
		t.Fatalf("url button = %v", live)
	}
	// Whether a button can be pressed at all is the daemon's answer, so the
	// card can draw a dead one as dead instead of letting somebody find out.
	dead := buttons[1].(map[string]any)
	if dead["kind"] != "reply" || dead["live"] != false {
		t.Fatalf("reply button = %v", dead)
	}

	sections, ok := interactive["sections"].([]any)
	if !ok || len(sections) != 1 {
		t.Fatalf("sections = %v", interactive["sections"])
	}
	rows := sections[0].(map[string]any)["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["title"] != "Reschedule" {
		t.Fatalf("rows = %v", rows)
	}
}

// A shared sticker pack is the one card in this family with something to do,
// and whether it can do it is joined from the library at read time rather than
// frozen into the row: installing a pack from the picker must not leave a card
// in the transcript still offering to add it.
func TestMessagesViewStickerPackJoinsTheLibrary(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	ctx := context.Background()
	chat := "c@s.whatsapp.net"
	id := "pack-msg-1"
	payload, err := store.EncodePayload(store.MessagePayload{StickerPack: &store.StickerPackPayload{
		PackID:    "pack-1",
		Name:      "Cats being unhelpful",
		Publisher: "Nobody in particular",
		Count:     12,
	}})
	if err != nil {
		t.Fatalf("encode pack: %v", err)
	}
	if _, err := db.SaveMediaMessage(ctx, store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:          id,
			ChatID:      chat,
			Timestamp:   time.Unix(1_700_000_000, 0),
			Direction:   store.DirectionIncoming,
			PayloadJSON: payload,
		},
		MediaKind:      store.MediaKindStickerPack,
		PayloadSummary: "Cats being unhelpful",
	}); err != nil {
		t.Fatalf("seed pack share: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"messages","chat_id":%q}`, chat))
	item := c.expectUpsert(sub, id)["item"].(map[string]any)
	pack, ok := item["sticker_pack"].(map[string]any)
	if !ok {
		t.Fatalf("sticker pack row missing its card: %v", item)
	}
	if pack["name"] != "Cats being unhelpful" || pack["count"] != float64(12) {
		t.Fatalf("pack = %v", pack)
	}
	// A pack the library has never heard of has nothing to install by id, and
	// a card that offered anyway would be offering a button that fails.
	if pack["installable"] != false || pack["installed"] != false {
		t.Fatalf("an unknown pack must not claim to be installable: %v", pack)
	}
}
