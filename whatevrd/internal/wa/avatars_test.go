package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestShouldSkipAvatarJIDSkipsInvalidZeroUser(t *testing.T) {
	jid := types.NewJID("0", types.DefaultUserServer)
	if !shouldSkipAvatarJID(jid) {
		t.Fatal("0@s.whatsapp.net should be skipped")
	}

	valid := types.NewJID("919999999999", types.DefaultUserServer)
	if shouldSkipAvatarJID(valid) {
		t.Fatal("valid user JID should not be skipped")
	}
}

func TestBareAvatarJIDStripsDevice(t *testing.T) {
	jid, err := types.ParseJID("919560965811:31@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	got := bareAvatarJID(jid).String()
	if got != "919560965811@s.whatsapp.net" {
		t.Fatalf("bareAvatarJID() = %q", got)
	}
}

func TestBareAvatarJIDStripsLIDDevice(t *testing.T) {
	jid, err := types.ParseJID("103903899181192:31@lid")
	if err != nil {
		t.Fatal(err)
	}

	got := bareAvatarJID(jid).String()
	if got != "103903899181192@lid" {
		t.Fatalf("bareAvatarJID() = %q", got)
	}
}
