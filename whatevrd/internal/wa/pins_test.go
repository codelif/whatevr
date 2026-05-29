package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestPinnedChatsFromEvents(t *testing.T) {
	kept := types.NewJID("111", types.DefaultUserServer)
	removed := types.NewJID("222", types.DefaultUserServer)
	ignored := types.NewJID("333", types.DefaultUserServer)

	pins := pinnedChatsFromEvents([]any{
		&events.Pin{JID: kept, Timestamp: time.Unix(123, 0), Action: &waSyncAction.PinAction{Pinned: proto.Bool(true)}},
		&events.Pin{JID: removed, Timestamp: time.Unix(456, 0), Action: &waSyncAction.PinAction{Pinned: proto.Bool(true)}},
		&events.Pin{JID: removed, Action: &waSyncAction.PinAction{Pinned: proto.Bool(false)}},
		&events.Pin{JID: ignored},
		"not a pin",
	})

	if len(pins) != 1 {
		t.Fatalf("len(pins) = %d, want 1", len(pins))
	}
	if got := pins[kept]; got != 123 {
		t.Fatalf("pins[kept] = %d, want 123", got)
	}
	if _, ok := pins[removed]; ok {
		t.Fatal("removed chat remained pinned")
	}
	if _, ok := pins[ignored]; ok {
		t.Fatal("pin event with nil action was included")
	}
}
