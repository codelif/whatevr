package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestArchivedChatsFromEvents(t *testing.T) {
	kept := types.NewJID("111", types.DefaultUserServer)
	unarchived := types.NewJID("222", types.DefaultUserServer)
	ignored := types.NewJID("333", types.DefaultUserServer)

	archived := archivedChatsFromEvents([]any{
		&events.Archive{JID: kept, Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}},
		&events.Archive{JID: unarchived, Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}},
		&events.Archive{JID: unarchived, Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(false)}},
		&events.Archive{JID: ignored},
		"not an archive",
	})

	if len(archived) != 1 {
		t.Fatalf("len(archived) = %d, want 1", len(archived))
	}
	if _, ok := archived[kept]; !ok {
		t.Fatal("kept chat was not archived")
	}
	if _, ok := archived[unarchived]; ok {
		t.Fatal("unarchived chat remained archived")
	}
	if _, ok := archived[ignored]; ok {
		t.Fatal("archive event with nil action was included")
	}
}

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
