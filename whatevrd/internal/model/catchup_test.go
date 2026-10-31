package model

// cases the catchup capture turned up, each a difference from the old core
// that the new one had wrong

import (
	"context"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/core"
)

// a group edit comes sealed with the target's secret, and what opens is a
// whole message around the edit, as whatsapp sends it
func TestSealedEditShowsTheNewText(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	target := &waE2E.Message{Conversation: proto.String("cook"), MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret}}
	edit := func(id, text string, sec int) core.Input {
		inner := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			Key:           &waCommon.MessageKey{RemoteJID: proto.String(grp), ID: proto.String("G9"), FromMe: proto.Bool(false), Participant: proto.String(boL)},
			EditedMessage: &waE2E.Message{Conversation: proto.String(text)},
			TimestampMS:   proto.Int64(at(sec).UnixMilli()),
		}}
		// edits carry no associated data, unlike votes
		key := hkdfutil.SHA256(secret, nil, []byte("G9"+boL+boL+useEdit), 32)
		iv := make([]byte, 12)
		iv[0] = byte(sec)
		payload, err := gcmutil.Encrypt(key, iv, pb(inner), nil)
		if err != nil {
			t.Fatal(err)
		}
		return msgIn(id, grp, boL, boPN, false, sec, &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
			TargetMessageKey: &waCommon.MessageKey{RemoteJID: proto.String(grp), ID: proto.String("G9"), FromMe: proto.Bool(false), Participant: proto.String(boL)},
			EncIV:            iv, EncPayload: payload, SecretEncType: waE2E.SecretEncryptedMessage_MESSAGE_EDIT.Enum(),
		}})
	}
	ins := []core.Input{msgIn("G9", grp, boL, boPN, false, 10, target), edit("E8", "coo", 11), edit("E9", "cool", 12)}
	for _, order := range [][]core.Input{ins, {ins[2], ins[1], ins[0]}, {ins[1], ins[0], ins[2]}} {
		db := openModel(t)
		feed(t, db, order)
		ms, err := NewReader(db.Read()).Messages(context.Background(), []string{grp}, Cursor{}, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for _, m := range ms {
			if m.ID == "G9" {
				got = m.Facts.Edit.GetConversation()
			}
		}
		if got != "cool" {
			t.Fatalf("edit reads %q, want the newest, cool", got)
		}
	}
}

// a message the phone sent again after it did not decrypt keeps the time it
// was sent, not the time of the resend
func TestResentMessageKeepsItsFirstTime(t *testing.T) {
	und := in(core.KindUndecryptable, core.UndecryptableHead{Source: core.Source{Chat: grp, Sender: boL, Group: true}, ID: "R9", T: at(10).Unix()}, nil, at(10))
	resent := msgIn("R9", grp, boL, boPN, false, 500, &waE2E.Message{Conversation: proto.String("again")})
	for _, order := range [][]core.Input{{und, resent}, {resent, und}} {
		db := openModel(t)
		feed(t, db, order)
		ms, err := NewReader(db.Read()).Messages(context.Background(), []string{grp}, Cursor{}, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) != 1 || ms[0].T != at(10).UnixMilli() || ms[0].Waiting {
			t.Fatalf("got %+v, want the message at its first time", ms)
		}
	}
}

// the phone reading a group comes as a plain read receipt from this account
func TestOwnReadReceiptClearsUnread(t *testing.T) {
	db := openModel(t)
	feed(t, db, []core.Input{
		msgIn("A1", grp, boL, boPN, false, 10, &waE2E.Message{Conversation: proto.String("one")}),
		msgIn("A2", grp, boL, boPN, false, 11, &waE2E.Message{Conversation: proto.String("two")}),
		in(core.KindReceipt, core.ReceiptHead{Source: core.Source{Chat: grp, Sender: meLID, FromMe: true, Group: true}, IDs: []string{"A1", "A2"}, Type: "read", T: at(20).Unix()}, nil, at(20)),
		msgIn("A3", grp, boL, boPN, false, 30, &waE2E.Message{Conversation: proto.String("three")}),
	})
	c, ok, err := NewReader(db.Read()).Chat(context.Background(), grp)
	if err != nil || !ok || c.Unread != 1 {
		t.Fatalf("unread %d (%v %v), want 1: only what came after the phone read", c.Unread, ok, err)
	}
}

// reading the message history was cut at ends history's count, the cut
// shares its millisecond
func TestReadAtTheHistoryCutClearsItsCount(t *testing.T) {
	db := openModel(t)
	feed(t, db, []core.Input{
		historyConv(ashaPN, webMsg("H1", ashaPN, false, "", 0, text("the last before the cut"))),
		in(core.KindReceipt, core.ReceiptHead{Source: core.Source{Chat: ashaPN, Sender: meLID, FromMe: true}, IDs: []string{"H1"}, Type: "read-self", T: at(60).Unix()}, nil, at(60)),
	})
	c, ok, err := NewReader(db.Read()).Chat(context.Background(), ashaPN)
	if err != nil || !ok || c.Unread != 0 {
		t.Fatalf("unread %d (%v %v), want 0: the message history counted is read", c.Unread, ok, err)
	}
}

// a group change is a transcript row; one that names this account brings
// the chat up and counts as unread, one that does not stays quiet
func TestGroupChangesAreRows(t *testing.T) {
	ctx := context.Background()
	db := openModel(t)
	other := "120363000000000077@g.us"
	feed(t, db, []core.Input{
		in(core.KindLIDMapping, core.LIDMappingHead{LID: meLID, PN: mePN, Self: true}, nil, at(0)),
		msgIn("Q1", grp, boL, boPN, false, 10, &waE2E.Message{Conversation: proto.String("hi")}),
		msgIn("Q2", other, boL, boPN, false, 11, &waE2E.Message{Conversation: proto.String("hey")}),
		in(core.KindGroupInfo, core.GroupInfoHead{JID: grp, T: at(20).Unix(), By: boL, Leave: []string{ashaL}}, nil, at(20)),
		in(core.KindGroupInfo, core.GroupInfoHead{JID: other, T: at(30).Unix(), By: boL, Join: []string{meLID}}, nil, at(30)),
		in(core.KindIdentityChange, core.IdentityChangeHead{JID: ashaL, T: at(40).Unix()}, nil, at(40)),
	})
	r := NewReader(db.Read())
	chats, err := r.Chats(ctx, ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	unread := map[string]int{}
	for _, c := range chats {
		order = append(order, c.Key)
		unread[c.Key] = c.Unread
	}
	// a changed security code alone makes no chat
	if !reflect.DeepEqual(order, []string{other, grp}) {
		t.Fatalf("list %q, want the group that added us first, and no chat for the security code", order)
	}
	if unread[other] != 2 || unread[grp] != 1 {
		t.Fatalf("unread %v, want 2 in the group that added us, 1 in the other", unread)
	}
	ms, err := r.Messages(ctx, []string{grp}, Cursor{}, 10, false)
	if err != nil || len(ms) != 2 || ms[0].System == nil || ms[0].System.Type != "group_leave" || ms[0].Sender != boL {
		t.Fatalf("transcript %+v %v", ms, err)
	}
	w, _ := r.World(ctx)
	if p, ok, _ := r.Preview(ctx, w, []string{grp}); !ok || p.ID != "Q1" {
		t.Fatalf("preview %+v, want the message, not the quiet leave", p)
	}
}

// a message deleted for everyone before it was read leaves the badge
func TestRevokedMessageIsNotUnread(t *testing.T) {
	db := openModel(t)
	feed(t, db, []core.Input{
		msgIn("R1", grp, boL, boPN, false, 10, &waE2E.Message{Conversation: proto.String("one")}),
		msgIn("R2", grp, boL, boPN, false, 11, &waE2E.Message{Conversation: proto.String("two")}),
		msgIn("R3", grp, boL, boPN, false, 12, &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: &waCommon.MessageKey{RemoteJID: proto.String(grp), ID: proto.String("R1"), Participant: proto.String(boL)}}}),
	})
	c, ok, err := NewReader(db.Read()).Chat(context.Background(), grp)
	if err != nil || !ok || c.Unread != 1 {
		t.Fatalf("unread %d (%v %v), want 1", c.Unread, ok, err)
	}
}

// a pin lasts as long as it says, a week when it says nothing
func TestPinLastsAsLongAsItSays(t *testing.T) {
	ctx := context.Background()
	db := openModel(t)
	pin := func(id, target string, at, secs int) core.Input {
		m := &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{
			Key:  &waCommon.MessageKey{RemoteJID: proto.String(grp), ID: proto.String(target), Participant: proto.String(boL)},
			Type: waE2E.PinInChatMessage_PIN_FOR_ALL.Enum(), SenderTimestampMS: proto.Int64(int64(at) * 1000)}}
		if secs > 0 {
			m.MessageContextInfo = &waE2E.MessageContextInfo{MessageAddOnDurationInSecs: proto.Uint32(uint32(secs))}
		}
		return msgIn(id, grp, boL, boPN, false, at, m)
	}
	feed(t, db, []core.Input{
		msgIn("P1", grp, boL, boPN, false, 10, &waE2E.Message{Conversation: proto.String("one")}),
		msgIn("P2", grp, boL, boPN, false, 11, &waE2E.Message{Conversation: proto.String("two")}),
		pin("X1", "P1", 20, 30*24*3600),
		pin("X2", "P2", 21, 0),
	})
	ms, err := NewReader(db.Read()).Pinned(ctx, []string{grp})
	if err != nil || len(ms) != 2 {
		t.Fatalf("pinned %+v %v", ms, err)
	}
	if got := ms[0].Facts.PinEnd - ms[0].Facts.PinT; got != 30*24*3600*1000 {
		t.Errorf("30 day pin lasts %d ms", got)
	}
	if got := ms[1].Facts.PinEnd - ms[1].Facts.PinT; got != 7*24*3600*1000 {
		t.Errorf("pin without a duration lasts %d ms", got)
	}
}

// one batch stamps its inputs alike, so two lids can take a number at the
// same instant; the later one by name holds it, and nothing fails
func TestLIDsArrivingTogetherDoNotFail(t *testing.T) {
	ctx := context.Background()
	db := openModel(t)
	pn := "919000000077@s.whatsapp.net"
	feed(t, db, []core.Input{
		in(core.KindLIDMapping, core.LIDMappingHead{LID: "100000000071@lid", PN: pn}, nil, at(1)),
		in(core.KindLIDMapping, core.LIDMappingHead{LID: "100000000072@lid", PN: pn}, nil, at(2)),
		in(core.KindLIDMapping, core.LIDMappingHead{LID: "100000000073@lid", PN: pn}, nil, at(2)),
	})
	w, err := NewReader(db.Read()).World(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Now(pn); got != "100000000073@lid" {
		t.Fatalf("%s is %s now", pn, got)
	}
}
