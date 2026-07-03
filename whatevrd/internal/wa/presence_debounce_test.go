package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatevrd/internal/app"
)

// The generation counter must invalidate a delayed-offline callback that lost
// a cancel race: a fired callback holding a stale generation is a no-op.
func TestPresenceOfflineTimerFiredStaleGeneration(t *testing.T) {
	client := &Client{daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}
	client.frontendSessions = map[string]frontendSession{"s1": {focused: false}}
	client.lastPresence = types.PresenceAvailable
	client.presenceTimerGen = 5

	client.presenceOfflineTimerFired(4)

	if client.lastPresence != types.PresenceAvailable {
		t.Fatalf("stale-generation callback changed lastPresence to %s", client.lastPresence)
	}
}

// A callback firing after the user refocused (desired presence back to
// available) must not push the account offline.
func TestPresenceOfflineTimerFiredRefocused(t *testing.T) {
	client := &Client{daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}
	client.frontendSessions = map[string]frontendSession{"s1": {focused: true}}
	client.lastPresence = types.PresenceAvailable
	client.presenceTimerGen = 5

	client.presenceOfflineTimerFired(5)

	if client.lastPresence != types.PresenceAvailable {
		t.Fatalf("callback pushed presence to %s despite a focused session", client.lastPresence)
	}
}

// Cancelling stops the pending timer and bumps the generation so an
// in-flight callback becomes stale.
func TestCancelPresenceOfflineTimer(t *testing.T) {
	client := &Client{daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}
	client.presenceOfflineTimer = time.AfterFunc(time.Hour, func() {})
	genBefore := client.presenceTimerGen

	client.presenceMu.Lock()
	client.cancelPresenceOfflineTimerLocked()
	client.presenceMu.Unlock()

	if client.presenceOfflineTimer != nil {
		t.Fatal("timer still armed after cancel")
	}
	if client.presenceTimerGen == genBefore {
		t.Fatal("generation not bumped by cancel")
	}
}
