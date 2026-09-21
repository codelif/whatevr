package wa

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// TestCallOfferTerminatesAsMissed locks in the calls contract: an offer rings
// (tracked pending, no chat message yet) and a terminate with no answer
// leaves a missed-call tombstone in the chat.
func TestCallOfferTerminatesAsMissed(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()
	from := types.JID{User: "5551234", Server: types.DefaultUserServer}

	client.handleCallOffer(ctx, &events.CallOffer{
		BasicCallMeta: types.BasicCallMeta{From: from, Timestamp: time.Now(), CallID: "call-1"},
	})
	if got := client.pendingCallForChat(from.String()); got == nil || got.callID != "call-1" {
		t.Fatalf("pending call = %+v, want call-1 ringing", got)
	}
	if ringing := client.daemon.RingingCalls(); len(ringing) != 1 || ringing[0].CallID != "call-1" {
		t.Fatalf("daemon ringing = %+v, want one call", ringing)
	}

	client.handleCallTerminate(ctx, &events.CallTerminate{
		BasicCallMeta: types.BasicCallMeta{From: from, Timestamp: time.Now(), CallID: "call-1"},
	})
	if got := client.pendingCallForChat(from.String()); got != nil {
		t.Fatalf("pending call = %+v, want cleared after terminate", got)
	}

	msgs, err := client.store.ListMessages(ctx, from.String(), 10, "")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "📞 Missed voice call" {
		t.Fatalf("chat messages = %+v, want one missed-call tombstone", msgs)
	}
}

// TestCallRejectDropsSilently locks in that a remotely-rejected call leaves
// no tombstone, and that rejecting an empty ring state is a no-op.
func TestCallRejectDropsSilently(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()
	from := types.JID{User: "5551234", Server: types.DefaultUserServer}

	client.handleCallOffer(ctx, &events.CallOffer{
		BasicCallMeta: types.BasicCallMeta{From: from, Timestamp: time.Now(), CallID: "call-2"},
	})
	client.handleCallReject(ctx, &events.CallReject{
		BasicCallMeta: types.BasicCallMeta{From: from, Timestamp: time.Now(), CallID: "call-2"},
	})
	if got := client.pendingCallForChat(from.String()); got != nil {
		t.Fatalf("pending call = %+v, want cleared after reject", got)
	}
	msgs, err := client.store.ListMessages(ctx, from.String(), 10, "")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("chat messages = %+v, want none after remote reject", msgs)
	}

	if err := client.RejectCall(ctx, "nobody@s.whatsapp.net"); err != nil {
		t.Fatalf("RejectCall on empty ring state = %v, want nil", err)
	}
	if err := client.RejectCall(ctx, ""); err == nil {
		t.Fatal("RejectCall with empty chat_id must fail")
	}
}
