package protocol

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A backgrounded network command must not stall the dispatch loop: while a
// chat.pin round trip is wedged, a later request on the same connection still
// answers (PROTOCOL.md allows responses out of order across requests), and the
// pin's own response lands once the round trip completes.
func TestBackgroundedCommandDoesNotStallConnection(t *testing.T) {
	actions := &fakeCommandActions{pinGate: make(chan struct{})}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":10,"method":"chat.pin","params":{"chat_id":"chat@s.whatsapp.net","pinned":true}}`)
	c.sendLine(`{"id":11,"method":"search.chats","params":{"query":"ali"}}`)

	msg := c.recv()
	if msg["id"] != float64(11) {
		t.Fatalf("expected the search response while the pin is in flight, got %v", msg)
	}
	if _, ok := msg["result"].(map[string]any); !ok {
		t.Fatalf("search failed: %v", msg)
	}

	close(actions.pinGate)
	msg = c.recv()
	if msg["id"] != float64(10) {
		t.Fatalf("expected the pin response after unblocking, got %v", msg)
	}
	if _, ok := msg["result"].(map[string]any); !ok {
		t.Fatalf("pin failed: %v", msg)
	}
	actions.mu.Lock()
	defer actions.mu.Unlock()
	if actions.pinnedChat != "chat@s.whatsapp.net" || !actions.pinned {
		t.Fatalf("pin call = %q/%v", actions.pinnedChat, actions.pinned)
	}
}

// A conn-tied backgrounded query's context must be cancelled when the
// connection goes away — a result nobody can receive is not worth finishing.
func TestConnCloseCancelsConnTiedQuery(t *testing.T) {
	actions := &fakeCommandActions{checkPhoneCtx: make(chan error, 1)}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":12,"method":"contacts.check_phone","params":{"phone":"+1 555 000 1111"}}`)
	c.conn.Close()

	select {
	case err := <-actions.checkPhoneCtx:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("query context was not cancelled on connection close")
	}
}

// Backgrounded mutations deliberately detach from the connection: a
// fire-and-forget client that sends and exits must not abort the mutation.
func TestBackgroundedMutationSurvivesConnClose(t *testing.T) {
	actions := &fakeCommandActions{pinGate: make(chan struct{})}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":13,"method":"chat.pin","params":{"chat_id":"chat@s.whatsapp.net","pinned":true}}`)
	// Wait for the handler goroutine to reach the gate, then hang up.
	time.Sleep(50 * time.Millisecond)
	c.conn.Close()
	close(actions.pinGate)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		actions.mu.Lock()
		done := actions.pinnedChat == "chat@s.whatsapp.net" && actions.pinned
		actions.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("mutation did not complete after the connection closed")
}
