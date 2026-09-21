package wa

import (
	"context"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	appstore "whatevrd/internal/store"
)

func statusIngestEvent(id, sender string, message *waE2E.Message) *events.Message {
	evt := mediaIngestEvent(id, message)
	evt.Info.Chat = types.StatusBroadcastJID
	evt.Info.Sender = types.JID{User: sender, Server: types.DefaultUserServer}
	evt.Info.MessageSource.IsGroup = false
	return evt
}

// TestStatusBroadcastBypassesChats locks in that status traffic lands in the
// status store (never as a chat message) for text and photo payloads, and
// that protocol noise is ignored.
func TestStatusBroadcastBypassesChats(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()

	textEvt := statusIngestEvent("S1", "5551234", &waE2E.Message{
		Conversation: proto.String("morning"),
	})
	if isStatusBroadcast(textEvt) != true {
		t.Fatal("isStatusBroadcast = false for status@broadcast")
	}
	client.handleMessage(ctx, textEvt, false)

	photoEvt := statusIngestEvent("S2", "5551234", &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			DirectPath: proto.String("/enc/photo.enc"),
			FileLength: proto.Uint64(4321),
		},
	})
	client.handleMessage(ctx, photoEvt, false)

	noiseEvt := statusIngestEvent("S3", "5551234", &waE2E.Message{
		ReactionMessage: &waE2E.ReactionMessage{},
	})
	client.handleMessage(ctx, noiseEvt, false)

	statuses, err := client.store.ListStatusUpdates(ctx, 0)
	if err != nil {
		t.Fatalf("list statuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("stored statuses = %d, want 2 (text + photo, noise skipped)", len(statuses))
	}
	if statuses[0].Kind != appstore.MediaKindImage || len(statuses[0].MediaPayload) == 0 {
		t.Fatalf("photo status = %+v, want image kind with keys", statuses[0])
	}
	if statuses[1].Kind != "text" || statuses[1].Text != "morning" {
		t.Fatalf("text status = %+v, want text/morning", statuses[1])
	}

	chat, err := client.store.GetChat(ctx, types.StatusBroadcastJID.String())
	if err == nil {
		t.Fatalf("status@broadcast materialized as chat %+v; statuses must never create chats", chat)
	}
}

// TestHandleUserStatusMuteEvent locks in the phone-mute mirror: a mute event
// stores the bare sender key, an unmute removes it, and nil action / empty
// JID events are skipped without touching the store.
func TestHandleUserStatusMuteEvent(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()

	peer := types.NewJID("5551234", types.DefaultUserServer)
	mute := &events.UserStatusMute{JID: peer, Action: &waSyncAction.UserStatusMuteAction{Muted: proto.Bool(true)}}
	client.handleUserStatusMuteEvent(ctx, mute)
	if muted, err := client.store.ListMutedStatusSenders(ctx); err != nil || len(muted) != 1 || muted[0] != peer.ToNonAD().String() {
		t.Fatalf("muted after phone mute = %v, %v; want [%s]", muted, err, peer.ToNonAD())
	}

	client.handleUserStatusMuteEvent(ctx, &events.UserStatusMute{JID: peer, Action: &waSyncAction.UserStatusMuteAction{Muted: proto.Bool(false)}})
	if muted, err := client.store.ListMutedStatusSenders(ctx); err != nil || len(muted) != 0 {
		t.Fatalf("muted after phone unmute = %v, %v; want []", muted, err)
	}

	// Nil action and empty JID must not write anything (or crash).
	client.handleUserStatusMuteEvent(ctx, &events.UserStatusMute{JID: peer})
	client.handleUserStatusMuteEvent(ctx, &events.UserStatusMute{Action: &waSyncAction.UserStatusMuteAction{Muted: proto.Bool(true)}})
	client.handleUserStatusMuteEvent(ctx, nil)
	if muted, err := client.store.ListMutedStatusSenders(ctx); err != nil || len(muted) != 0 {
		t.Fatalf("muted after skipped events = %v, %v; want []", muted, err)
	}
}

// TestStatusMuteKeys locks in the store-key derivation: device suffixes are
// stripped to the bare form status rows use, and empty JIDs yield no keys.
func TestStatusMuteKeys(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()

	withDevice := types.JID{User: "5551234", Server: types.DefaultUserServer, Device: 42}
	keys := client.statusMuteKeys(ctx, withDevice)
	if len(keys) == 0 || keys[0] != "5551234@s.whatsapp.net" {
		t.Fatalf("mute keys for device JID = %v; want bare first", keys)
	}
	if keys := client.statusMuteKeys(ctx, types.EmptyJID); len(keys) != 0 {
		t.Fatalf("mute keys for empty JID = %v; want none", keys)
	}
}

// TestSameStringSet locks in the reconcile skip: order and blanks do not
// count as differences.
func TestSameStringSet(t *testing.T) {
	if !sameStringSet([]string{"b", "a"}, []string{"a", "b"}) {
		t.Fatal("reordered sets reported different")
	}
	if !sameStringSet([]string{"a", "  "}, []string{"a"}) {
		t.Fatal("blank ids reported different")
	}
	if sameStringSet([]string{"a"}, []string{"a", "b"}) {
		t.Fatal("different sets reported same")
	}
	if !sameStringSet(nil, nil) {
		t.Fatal("empty sets reported different")
	}
}
