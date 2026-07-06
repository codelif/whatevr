package protocol

import (
	"context"
	"testing"

	"whatevrd/internal/app"
)

type fakeSenderResolver struct {
	names map[string]string
}

func (f fakeSenderResolver) SenderDisplay(_ context.Context, id string) (string, string, error) {
	return f.names[id], "", nil
}

// typingSenders pulls the (jid, name) pairs out of a typing upsert item.
func typingSenders(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	item, ok := msg["item"].(map[string]any)
	if !ok {
		t.Fatalf("typing upsert without an item: %v", msg)
	}
	raw, ok := item["senders"].([]any)
	if !ok {
		t.Fatalf("typing item without a senders list: %v", msg)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, s := range raw {
		out = append(out, s.(map[string]any))
	}
	return out
}

func TestTypingViewInitialAndLiveUpdates(t *testing.T) {
	socketPath, server := startTestServer(t)
	resolver := fakeSenderResolver{names: map[string]string{
		"alice@s.whatsapp.net": "Alice",
		"bob@s.whatsapp.net":   "Bob",
	}}
	server.RegisterView("typing", typingView{daemon: server.daemon, resolver: resolver})

	// Someone is already composing before the frontend subscribes: it must land
	// in the initial fill (snapshot), not only via a later live event.
	server.daemon.PublishChatPresence("chatA", "alice@s.whatsapp.net", true)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"typing"}`)

	msg := c.expectUpsert(sub, "chatA")
	senders := typingSenders(t, msg)
	if len(senders) != 1 || senders[0]["jid"] != "alice@s.whatsapp.net" || senders[0]["name"] != "Alice" {
		t.Fatalf("initial typing senders = %v", senders)
	}
	// Unwindowed collection: nothing left to extend into, so exhausted.
	c.expectReady(sub, true)

	// A second chat starts composing: a fresh upsert keyed by that chat id.
	server.daemon.PublishChatPresence("chatB", "bob@s.whatsapp.net", true)
	msg = c.expectUpsert(sub, "chatB")
	if senders := typingSenders(t, msg); senders[0]["name"] != "Bob" {
		t.Fatalf("chatB typing senders = %v", senders)
	}

	// An availability event carries no sender and must not touch the typing
	// view. Publish one, then stop composing in chatA; the next event the client
	// sees must be the chatA remove, proving the availability event produced
	// nothing in between.
	server.daemon.PublishContactAvailability("chatA", app.ContactAvailabilityOnline, 1720000000)
	server.daemon.PublishChatPresence("chatA", "alice@s.whatsapp.net", false)
	c.expectRemove(sub, "chatA")
}

// A frontend that subscribes while nobody is composing gets an empty view that
// readies immediately, then follows live composing events.
func TestTypingViewEmptyThenLive(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("typing", typingView{daemon: server.daemon, resolver: fakeSenderResolver{}})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"typing"}`)
	c.expectReady(sub, true)

	server.daemon.PublishChatPresence("chatX", "someone@s.whatsapp.net", true)
	msg := c.expectUpsert(sub, "chatX")
	// No resolver entry: the sender still carries its jid, name is omitted.
	senders := typingSenders(t, msg)
	if senders[0]["jid"] != "someone@s.whatsapp.net" {
		t.Fatalf("chatX sender jid = %v", senders)
	}
	if _, hasName := senders[0]["name"]; hasName {
		t.Fatalf("unresolved sender should omit name: %v", senders)
	}
}
