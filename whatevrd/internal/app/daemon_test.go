package app

import (
	"testing"
	"time"
)

func TestPublishChatPresenceAutoClearsStaleComposing(t *testing.T) {
	withComposingPresenceTTL(t, 20*time.Millisecond)
	d := NewDaemon(Paths{})
	events, cancel := d.SubscribeDaemonEvents()
	defer cancel()
	drainInitialDaemonEvent(t, events)

	d.PublishChatPresence("chat-1", "sender-1", true)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", true)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", false)
}

func TestPublishChatPresenceRefreshPreventsOlderExpiry(t *testing.T) {
	ttl := 30 * time.Millisecond
	withComposingPresenceTTL(t, ttl)
	d := NewDaemon(Paths{})
	events, cancel := d.SubscribeDaemonEvents()
	defer cancel()
	drainInitialDaemonEvent(t, events)

	d.PublishChatPresence("chat-1", "sender-1", true)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", true)
	time.Sleep(ttl / 2)

	d.PublishChatPresence("chat-1", "sender-1", true)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", true)
	assertNoDaemonEvent(t, events, ttl/2+10*time.Millisecond)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", false)
}

func TestPublishChatPresencePausedClearsImmediately(t *testing.T) {
	withComposingPresenceTTL(t, time.Second)
	d := NewDaemon(Paths{})
	events, cancel := d.SubscribeDaemonEvents()
	defer cancel()
	drainInitialDaemonEvent(t, events)

	d.PublishChatPresence("chat-1", "sender-1", true)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", true)
	d.PublishChatPresence("chat-1", "sender-1", false)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", false)
}

func TestClearChatComposingClearsMatchingSender(t *testing.T) {
	withComposingPresenceTTL(t, time.Second)
	d := NewDaemon(Paths{})
	events, cancel := d.SubscribeDaemonEvents()
	defer cancel()
	drainInitialDaemonEvent(t, events)

	d.PublishChatPresence("chat-1", "sender-1", true)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", true)
	if !d.ClearChatComposing("chat-1", "sender-1") {
		t.Fatal("ClearChatComposing returned false")
	}
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", false)
}

func TestClearChatComposingKeepsDifferentSender(t *testing.T) {
	withComposingPresenceTTL(t, time.Second)
	d := NewDaemon(Paths{})
	events, cancel := d.SubscribeDaemonEvents()
	defer cancel()
	drainInitialDaemonEvent(t, events)

	d.PublishChatPresence("chat-1", "sender-1", true)
	assertChatPresenceEvent(t, nextDaemonEvent(t, events), "chat-1", "sender-1", true)
	if d.ClearChatComposing("chat-1", "sender-2") {
		t.Fatal("ClearChatComposing returned true for different sender")
	}
	assertNoDaemonEvent(t, events, 20*time.Millisecond)
}

func TestClearChatComposingReturnsFalseWhenNotComposing(t *testing.T) {
	d := NewDaemon(Paths{})
	events, cancel := d.SubscribeDaemonEvents()
	defer cancel()
	drainInitialDaemonEvent(t, events)

	if d.ClearChatComposing("chat-1", "sender-1") {
		t.Fatal("ClearChatComposing returned true without composing state")
	}
	assertNoDaemonEvent(t, events, 20*time.Millisecond)
}

func TestHasActiveHistorySyncTracksIncompleteProgress(t *testing.T) {
	d := NewDaemon(Paths{})
	if d.HasActiveHistorySync() {
		t.Fatal("new daemon has active history sync")
	}

	d.PublishHistorySyncProgress(HistorySyncEvent{SyncType: HistorySyncTypeRecent, ProgressPercent: 43})
	if !d.HasActiveHistorySync() {
		t.Fatal("incomplete history sync was not marked active")
	}

	d.PublishHistorySyncProgress(HistorySyncEvent{SyncType: HistorySyncTypeRecent, ProgressPercent: 100, IsComplete: true})
	if d.HasActiveHistorySync() {
		t.Fatal("complete history sync was still marked active")
	}
}

func withComposingPresenceTTL(t *testing.T, ttl time.Duration) {
	t.Helper()
	previous := composingPresenceTTL
	composingPresenceTTL = ttl
	t.Cleanup(func() {
		composingPresenceTTL = previous
	})
}

func drainInitialDaemonEvent(t *testing.T, events <-chan DaemonEvent) {
	t.Helper()
	event := nextDaemonEvent(t, events)
	if event.Kind != DaemonEventConnectionChanged {
		t.Fatalf("initial event kind = %v, want %v", event.Kind, DaemonEventConnectionChanged)
	}
}

func nextDaemonEvent(t *testing.T, events <-chan DaemonEvent) DaemonEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for daemon event")
		return DaemonEvent{}
	}
}

func assertNoDaemonEvent(t *testing.T, events <-chan DaemonEvent, timeout time.Duration) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected daemon event: %+v", event)
	case <-time.After(timeout):
	}
}

func assertChatPresenceEvent(t *testing.T, event DaemonEvent, chatID, senderID string, isComposing bool) {
	t.Helper()
	if event.Kind != DaemonEventChatPresence {
		t.Fatalf("event kind = %v, want %v", event.Kind, DaemonEventChatPresence)
	}
	if event.Chat.ID != chatID {
		t.Fatalf("chat ID = %q, want %q", event.Chat.ID, chatID)
	}
	if event.SenderID != senderID {
		t.Fatalf("sender ID = %q, want %q", event.SenderID, senderID)
	}
	if event.IsComposing != isComposing {
		t.Fatalf("is composing = %t, want %t", event.IsComposing, isComposing)
	}
}
