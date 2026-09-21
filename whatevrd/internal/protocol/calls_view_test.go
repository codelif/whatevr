package protocol

import (
	"testing"

	"whatevrd/internal/app"
)

// The calls view lists locally-ringing calls with caller and chat, and drops
// them when the daemon clears the ring state.
func TestCallsViewTracksRinging(t *testing.T) {
	socketPath, daemon, _ := startChatsTestServer(t)
	daemon.SetRingingCall(app.RingingCall{
		CallID:      "call-9",
		ChatID:      "c@s.whatsapp.net",
		CallerID:    "c@s.whatsapp.net",
		StartedUnix: 1_700_000_000,
	})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"calls"}`)
	item := c.expectUpsert(sub, "call-9")["item"].(map[string]any)
	if item["chat_id"] != "c@s.whatsapp.net" {
		t.Fatalf("chat_id = %v, want the ringing chat", item["chat_id"])
	}
	caller := item["caller"].(map[string]any)
	if caller["id"] != "c@s.whatsapp.net" {
		t.Fatalf("caller = %v, want the caller", caller)
	}
	c.expectReady(sub, true)

	daemon.ClearRingingCall("call-9")
	daemon.PublishCallChanged("call-9", "c@s.whatsapp.net")
	c.expectRemove(sub, "call-9")
}
