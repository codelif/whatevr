package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	appstore "whatevrd/internal/store"
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

func TestAvatarQueuePriorityAndPromotion(t *testing.T) {
	c := &Client{}
	bgSubject := appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: "bg@s.whatsapp.net"}
	visSubject := appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: "vis@s.whatsapp.net"}

	if !c.enqueueAvatar(bgSubject, false, avatarPriorityBackground) {
		t.Fatal("background enqueue failed")
	}
	if !c.enqueueAvatar(visSubject, false, avatarPriorityVisible) {
		t.Fatal("visible enqueue failed")
	}
	// Duplicate enqueue must dedupe, not double-queue.
	if !c.enqueueAvatar(visSubject, false, avatarPriorityVisible) {
		t.Fatal("duplicate visible enqueue failed")
	}

	job, priority, ok := c.popAvatarJob()
	if !ok || priority != avatarPriorityVisible || job.subject != visSubject {
		t.Fatalf("first pop = %+v (%d, %t), want visible subject first", job, priority, ok)
	}
	job, priority, ok = c.popAvatarJob()
	if !ok || priority != avatarPriorityBackground || job.subject != bgSubject {
		t.Fatalf("second pop = %+v (%d, %t), want background subject", job, priority, ok)
	}
	if _, _, ok := c.popAvatarJob(); ok {
		t.Fatal("queue should be empty after two pops")
	}

	// A visible request for an already-queued background subject promotes it.
	if !c.enqueueAvatar(bgSubject, false, avatarPriorityBackground) {
		t.Fatal("background re-enqueue failed")
	}
	if !c.enqueueAvatar(bgSubject, true, avatarPriorityVisible) {
		t.Fatal("promoting enqueue failed")
	}
	job, priority, ok = c.popAvatarJob()
	if !ok || priority != avatarPriorityVisible || job.subject != bgSubject || !job.force {
		t.Fatalf("promoted pop = %+v (%d, %t), want forced visible job", job, priority, ok)
	}
	if len(c.avatarLow) != 0 {
		t.Fatalf("background queue still has %d jobs after promotion", len(c.avatarLow))
	}
}

func TestAvatarTransientErrorBackoff(t *testing.T) {
	cases := []struct {
		retries int32
		want    time.Duration
	}{
		{0, time.Minute},
		{1, 2 * time.Minute},
		{3, 8 * time.Minute},
		{10, time.Hour},
		{-1, time.Minute},
	}
	for _, tc := range cases {
		if got := avatarTransientErrorBackoff(tc.retries); got != tc.want {
			t.Errorf("avatarTransientErrorBackoff(%d) = %s, want %s", tc.retries, got, tc.want)
		}
	}
}
