package protocol

import (
	"context"
	"sync"
	"testing"

	"whatevrd/internal/app"
)

// fakePresenceActions records the chats a presence view asked to subscribe to
// upstream, standing in for *wa.Client.
type fakePresenceActions struct {
	mu        sync.Mutex
	subscribed []string
}

func (f *fakePresenceActions) SubscribeChatPresence(_ context.Context, chatID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribed = append(f.subscribed, chatID)
	return nil
}

func (f *fakePresenceActions) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.subscribed...)
}

// presenceFields pulls availability + last_seen out of a presence upsert item.
func presenceFields(t *testing.T, msg map[string]any) (string, float64, bool) {
	t.Helper()
	item, ok := msg["item"].(map[string]any)
	if !ok {
		t.Fatalf("presence upsert without an item: %v", msg)
	}
	avail, _ := item["availability"].(string)
	last, hasLast := item["last_seen_unix"].(float64)
	return avail, last, hasLast
}

// Subscribing drives the upstream presence subscribe, then availability events
// flow in live as ordinary upserts.
func TestPresenceViewSubscribeTriggersUpstreamThenLive(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := &fakePresenceActions{}
	server.RegisterView("presence", presenceView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"presence","chat_id":"aditi@s.whatsapp.net"}`)

	// Nothing cached yet: the view readies empty (no availability observed).
	c.expectReady(sub, true)

	// Subscribing must have asked WhatsApp to start delivering presence.
	waitFor(t, "upstream presence subscribe", func() bool {
		calls := actions.calls()
		return len(calls) == 1 && calls[0] == "aditi@s.whatsapp.net"
	})

	// Availability lands upstream: an upsert keyed by the participant jid.
	server.daemon.PublishContactAvailability("aditi@s.whatsapp.net", app.ContactAvailabilityOnline, 0)
	msg := c.expectUpsert(sub, "aditi@s.whatsapp.net")
	if avail, _, hasLast := presenceFields(t, msg); avail != "online" || hasLast {
		t.Fatalf("online presence = %v (last_seen present=%v)", avail, hasLast)
	}

	// Goes offline with a last-seen: another upsert, now carrying last_seen.
	server.daemon.PublishContactAvailability("aditi@s.whatsapp.net", app.ContactAvailabilityOffline, 1720000000)
	msg = c.expectUpsert(sub, "aditi@s.whatsapp.net")
	if avail, last, hasLast := presenceFields(t, msg); avail != "offline" || !hasLast || last != 1720000000 {
		t.Fatalf("offline presence = %v last_seen=%v (present=%v)", avail, last, hasLast)
	}
}

// Availability already cached for the chat (an earlier subscription in this
// daemon session) seeds the initial fill synchronously, before ready.
func TestPresenceViewInitialFillFromCache(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("presence", presenceView{daemon: server.daemon, actions: &fakePresenceActions{}})

	server.daemon.PublishContactAvailability("bob@s.whatsapp.net", app.ContactAvailabilityOffline, 1719999999)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"presence","chat_id":"bob@s.whatsapp.net"}`)

	msg := c.expectUpsert(sub, "bob@s.whatsapp.net")
	if avail, last, hasLast := presenceFields(t, msg); avail != "offline" || !hasLast || last != 1719999999 {
		t.Fatalf("cached presence = %v last_seen=%v (present=%v)", avail, last, hasLast)
	}
	c.expectReady(sub, true)
}

// A composing event (the SenderID-set half of the overloaded chat-presence
// event) belongs to the `typing` view and must not touch `presence`. Publish
// one, then a real availability event; the client's next event must be the
// availability upsert, proving the composing event produced nothing between.
func TestPresenceViewIgnoresComposingEvents(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("presence", presenceView{daemon: server.daemon, actions: &fakePresenceActions{}})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"presence","chat_id":"carol@s.whatsapp.net"}`)
	c.expectReady(sub, true)

	server.daemon.PublishChatPresence("carol@s.whatsapp.net", "carol@s.whatsapp.net", true)
	server.daemon.PublishContactAvailability("carol@s.whatsapp.net", app.ContactAvailabilityOnline, 0)

	msg := c.expectUpsert(sub, "carol@s.whatsapp.net")
	if avail, _, _ := presenceFields(t, msg); avail != "online" {
		t.Fatalf("expected online availability, got %v", avail)
	}
}

// Availability for a *different* chat must not leak into this subscription.
func TestPresenceViewScopedToChat(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("presence", presenceView{daemon: server.daemon, actions: &fakePresenceActions{}})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"presence","chat_id":"dan@s.whatsapp.net"}`)
	c.expectReady(sub, true)

	// A different chat's availability, then this chat's: the first event the
	// client sees must be dan's, proving the other chat was filtered out.
	server.daemon.PublishContactAvailability("eve@s.whatsapp.net", app.ContactAvailabilityOnline, 0)
	server.daemon.PublishContactAvailability("dan@s.whatsapp.net", app.ContactAvailabilityOnline, 0)
	c.expectUpsert(sub, "dan@s.whatsapp.net")
}

func TestPresenceViewRequiresChatID(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("presence", presenceView{daemon: server.daemon, actions: &fakePresenceActions{}})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"presence"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("missing chat_id error code = %q, want %q", code, CodeInvalidParams)
	}
}
