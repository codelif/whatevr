package protocol

import (
	"context"
	"fmt"
	"testing"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// starDB stars/unstars a stored message and publishes the update the way the
// real star path does.
func starDB(t *testing.T, daemon *app.Daemon, db *store.DB, chat, id string, starred bool) {
	t.Helper()
	if _, _, err := db.SetMessageStarred(context.Background(), id, starred); err != nil {
		t.Fatalf("set starred %q: %v", id, err)
	}
	daemon.PublishMessageUpdated(app.Message{ID: id, ChatID: chat})
}

// pinDB pins/unpins a stored message (pinnedUntil 0 unpins) and publishes.
func pinDB(t *testing.T, daemon *app.Daemon, db *store.DB, chat, id string, pinnedAt, pinnedUntil int64) {
	t.Helper()
	if _, _, err := db.SetMessagePinned(context.Background(), id, pinnedAt, pinnedUntil); err != nil {
		t.Fatalf("set pinned %q: %v", id, err)
	}
	daemon.PublishMessageUpdated(app.Message{ID: id, ChatID: chat})
}

// starred: fill from existing stars newest-first, then a live star/unstar.
func TestStarredViewFillAndLiveUpdate(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	idOne := seedTextMessage(t, db, chat, "one", base)
	idTwo := seedTextMessage(t, db, chat, "two", base.Add(time.Minute))
	idThree := seedTextMessage(t, db, chat, "three", base.Add(2*time.Minute))

	// Star two and three before subscribing.
	if _, _, err := db.SetMessageStarred(context.Background(), idTwo, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SetMessageStarred(context.Background(), idThree, true); err != nil {
		t.Fatal(err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"starred"}`)

	// Newest starred first: three, then two, in both delivery and sort order.
	newest := c.expectUpsert(sub, idThree)
	older := c.expectUpsert(sub, idTwo)
	if newest["sort"].(string) >= older["sort"].(string) {
		t.Fatalf("starred sort order = %q then %q, want newest first", newest["sort"], older["sort"])
	}
	c.expectReady(sub, true)

	// Star the oldest live: it joins the (unwindowed) window.
	starDB(t, daemon, db, chat, idOne, true)
	c.expectUpsert(sub, idOne)

	// Unstar two: it falls out.
	starDB(t, daemon, db, chat, idTwo, false)
	c.expectRemove(sub, idTwo)
}

// starred windowed: a limit gives a live-edge window that extend-older grows.
func TestStarredViewWindowedExtend(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	idTwo := seedTextMessage(t, db, chat, "two", base.Add(time.Minute))
	idThree := seedTextMessage(t, db, chat, "three", base.Add(2*time.Minute))
	// An unstarred message must never appear.
	seedTextMessage(t, db, chat, "unstarred", base.Add(3*time.Minute))
	for _, id := range []string{idTwo, idThree} {
		if _, _, err := db.SetMessageStarred(context.Background(), id, true); err != nil {
			t.Fatal(err)
		}
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"starred","limit":1}`)
	c.expectUpsert(sub, idThree) // newest only
	c.expectReady(sub, false)    // more to reach

	c.extend(3, sub, 1, "older")
	c.expectUpsert(sub, idTwo)
	c.expectReady(sub, true) // both stars now in the window
}

// starred scoped to one chat: only that chat's stars appear.
func TestStarredViewChatScoped(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chatA := "a@s.whatsapp.net"
	chatB := "b@s.whatsapp.net"
	idA := seedTextMessage(t, db, chatA, "in A", base)
	idB := seedTextMessage(t, db, chatB, "in B", base.Add(time.Minute))
	for _, id := range []string{idA, idB} {
		if _, _, err := db.SetMessageStarred(context.Background(), id, true); err != nil {
			t.Fatal(err)
		}
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"starred","chat_id":%q}`, chatA))
	// Only chat A's star: the first (and only) upsert must be idA, then ready.
	c.expectUpsert(sub, idA)
	c.expectReady(sub, true)
}

// pinned: fill oldest-pin-first, then a live pin and unpin.
func TestPinnedViewFillAndLiveUpdate(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	base := time.Unix(1_700_000_000, 0)
	chat := "c@s.whatsapp.net"
	idOne := seedTextMessage(t, db, chat, "one", base)
	idTwo := seedTextMessage(t, db, chat, "two", base.Add(time.Minute))
	idThree := seedTextMessage(t, db, chat, "three", base.Add(2*time.Minute))

	far := time.Now().Add(time.Hour).Unix()
	// Pin one (earlier pin time) then three (later pin time).
	if _, _, err := db.SetMessagePinned(context.Background(), idOne, 1000, far); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SetMessagePinned(context.Background(), idThree, 2000, far); err != nil {
		t.Fatal(err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"pinned","chat_id":%q}`, chat))
	// Oldest pin first: one, then three, in both delivery and sort order.
	oldestPin := c.expectUpsert(sub, idOne)
	newerPin := c.expectUpsert(sub, idThree)
	if oldestPin["sort"].(string) >= newerPin["sort"].(string) {
		t.Fatalf("pinned sort order = %q then %q, want oldest pin first", oldestPin["sort"], newerPin["sort"])
	}
	c.expectReady(sub, true)

	// Pin two live.
	pinDB(t, daemon, db, chat, idTwo, 3000, far)
	c.expectUpsert(sub, idTwo)

	// Unpin one.
	pinDB(t, daemon, db, chat, idOne, 0, 0)
	c.expectRemove(sub, idOne)
}

// pinned expiry: a pin whose pinned_until passes falls out with no daemon event.
func TestPinnedViewExpiry(t *testing.T) {
	socketPath, _, db := startChatsTestServer(t)
	chat := "c@s.whatsapp.net"
	id := seedTextMessage(t, db, chat, "temporary", time.Unix(1_700_000_000, 0))
	// Pin until one second from now.
	until := time.Now().Add(1 * time.Second).Unix()
	if _, _, err := db.SetMessagePinned(context.Background(), id, 1000, until); err != nil {
		t.Fatal(err)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, fmt.Sprintf(`{"view":"pinned","chat_id":%q}`, chat))
	c.expectUpsert(sub, id)
	c.expectReady(sub, true)

	// The armed expiry timer fires ~250ms after pinned_until, re-reads, and the
	// now-expired row falls out.
	c.expectRemove(sub, id)
}

func TestPinnedViewRequiresChatID(t *testing.T) {
	socketPath, _, _ := startChatsTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"pinned"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("missing chat_id error code = %q, want %q", code, CodeInvalidParams)
	}
}
