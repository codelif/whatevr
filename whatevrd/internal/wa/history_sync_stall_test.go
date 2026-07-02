package wa

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func stallTimerArmed(c *Client) bool {
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	return c.historySyncStallTimer != nil
}

// A message-carrying sync that goes quiet below 100% must fire one STALLED
// event carrying the last known position; recent activity re-arms the
// watchdog instead of firing, on-demand responses never arm it, and clearing
// (sync complete) disarms it.
func TestHistorySyncStallWatchdog(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	daemon := app.NewDaemon(app.Paths{})
	client := &Client{store: db, daemon: daemon, log: waLog.Noop}
	events, unsubscribe := daemon.SubscribeDaemonEvents()
	defer unsubscribe()

	client.noteHistorySyncActivity(app.HistorySyncEvent{SyncType: app.HistorySyncTypeOnDemand})
	if stallTimerArmed(client) {
		t.Fatal("on-demand activity armed the stall watchdog")
	}

	recent := app.HistorySyncEvent{
		SyncType:        app.HistorySyncTypeRecent,
		ProgressPercent: 26,
		ChunkOrder:      12,
		Phase:           app.HistorySyncPhaseQueued,
	}
	client.noteHistorySyncActivity(recent)
	if !stallTimerArmed(client) {
		t.Fatal("recent-sync activity did not arm the stall watchdog")
	}

	// Fresh activity: the check must re-arm for the remaining idle window,
	// not declare a stall.
	client.historySyncStallCheck()
	if !stallTimerArmed(client) {
		t.Fatal("stall check with fresh activity did not re-arm the watchdog")
	}

	// Phone quiet past the timeout: one STALLED event with the last position.
	client.historySyncMu.Lock()
	client.historySyncLastActivity = time.Now().Add(-2 * historySyncStallTimeout)
	if client.historySyncStallTimer != nil {
		client.historySyncStallTimer.Stop()
		client.historySyncStallTimer = nil
	}
	client.historySyncMu.Unlock()
	client.historySyncStallCheck()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Kind != app.DaemonEventHistorySyncProgress || evt.HistorySync.Phase != app.HistorySyncPhaseStalled {
				continue
			}
			if evt.HistorySync.SyncType != app.HistorySyncTypeRecent ||
				evt.HistorySync.ChunkOrder != 12 ||
				evt.HistorySync.ProgressPercent != 26 {
				t.Fatalf("stalled event = %+v, want recent sync chunk 12 at 26%%", evt.HistorySync)
			}
		case <-deadline:
			t.Fatal("no STALLED history sync event was published")
		}
		break
	}
	if stallTimerArmed(client) {
		t.Fatal("watchdog still armed after declaring a stall")
	}

	// A resumed chunk re-arms; completion clears.
	client.noteHistorySyncActivity(recent)
	if !stallTimerArmed(client) {
		t.Fatal("resumed activity did not re-arm the watchdog")
	}
	client.clearHistorySyncStallWatch()
	if stallTimerArmed(client) {
		t.Fatal("clearHistorySyncStallWatch left the watchdog armed")
	}
}
