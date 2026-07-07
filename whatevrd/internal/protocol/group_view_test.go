package protocol

import (
	"context"
	"errors"
	"sync"
	"testing"

	"whatevrd/internal/app"
)

// fakeGroups is a mutable GroupActions backend standing in for *wa.Client's
// store+network group resolution: tests set the card a chat_id resolves to.
type fakeGroups struct {
	mu     sync.Mutex
	byChat map[string]app.GroupInfo
}

func newFakeGroups() *fakeGroups {
	return &fakeGroups{byChat: map[string]app.GroupInfo{}}
}

func (f *fakeGroups) GetGroupInfo(_ context.Context, chatID string) (app.GroupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.byChat[chatID]
	if !ok {
		return app.GroupInfo{}, errors.New("invalid group jid")
	}
	return info, nil
}

func (f *fakeGroups) set(chatID string, info app.GroupInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byChat[chatID] = info
}

func itemInt(t *testing.T, msg map[string]any, key string) float64 {
	t.Helper()
	item, ok := msg["item"].(map[string]any)
	if !ok {
		t.Fatalf("upsert without an item: %v", msg)
	}
	n, _ := item[key].(float64)
	return n
}

func itemBool(t *testing.T, msg map[string]any, key string) bool {
	t.Helper()
	item, ok := msg["item"].(map[string]any)
	if !ok {
		t.Fatalf("upsert without an item: %v", msg)
	}
	b, _ := item[key].(bool)
	return b
}

// group: local card at subscribe (subject + member_count, no roles/flags), then
// the live fetch fills owner/my_role/announce/locked and refreshes members.
func TestGroupTwoPhase(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeGroups()
	actions.set("fam@g.us", app.GroupInfo{
		Subject: "Family",
		Members: []app.GroupMember{
			{JID: "a@s.whatsapp.net", DisplayName: "Alice"},
			{JID: "b@s.whatsapp.net", DisplayName: "Bob"},
		},
	})
	server.RegisterView("group", groupView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"group","chat_id":"fam@g.us"}`)

	first := c.expectUpsert(sub, "fam@g.us")
	if s := itemField(t, first, "subject"); s != "Family" {
		t.Fatalf("subject = %q", s)
	}
	if n := itemInt(t, first, "member_count"); n != 2 {
		t.Fatalf("member_count = %v, want 2", n)
	}
	if r := itemField(t, first, "my_role"); r != "" {
		t.Fatalf("my_role should be empty pre-live, got %q", r)
	}
	c.expectReady(sub, true)

	// Live fetch lands: description, owner, my_role, announce/locked, +a member.
	server.daemon.PublishGroupInfoUpdated("fam@g.us", app.GroupInfo{
		Subject:     "Family",
		Description: "our group",
		OwnerJID:    "a@s.whatsapp.net",
		MyRole:      "admin",
		IsAnnounce:  true,
		IsLocked:    true,
		Members: []app.GroupMember{
			{JID: "a@s.whatsapp.net", DisplayName: "Alice", IsAdmin: true, IsSuperAdmin: true},
			{JID: "b@s.whatsapp.net", DisplayName: "Bob"},
			{JID: "me@s.whatsapp.net", DisplayName: "Me", IsAdmin: true},
		},
	})
	upd := c.expectUpsert(sub, "fam@g.us")
	if r := itemField(t, upd, "my_role"); r != "admin" {
		t.Fatalf("my_role = %q, want admin", r)
	}
	if o := itemField(t, upd, "owner"); o != "a@s.whatsapp.net" {
		t.Fatalf("owner = %q", o)
	}
	if !itemBool(t, upd, "announce") || !itemBool(t, upd, "locked") {
		t.Fatalf("announce/locked not set: %v", upd)
	}
	if n := itemInt(t, upd, "member_count"); n != 3 {
		t.Fatalf("member_count after live = %v, want 3", n)
	}
	if d := itemField(t, upd, "description"); d != "our group" {
		t.Fatalf("description = %q", d)
	}
}

// group avatar refresh (a chat-kind avatar) overlays the path.
func TestGroupAvatarOverlay(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeGroups()
	actions.set("fam@g.us", app.GroupInfo{Subject: "Family"})
	server.RegisterView("group", groupView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"group","chat_id":"fam@g.us"}`)
	c.expectUpsert(sub, "fam@g.us")
	c.expectReady(sub, true)

	// A sender-kind avatar for a member must NOT touch the group card.
	server.daemon.PublishAvatarUpdated(app.Avatar{Kind: app.AvatarSubjectKindSender, ID: "a@s.whatsapp.net", LocalPath: "/x"})
	// The chat-kind avatar for this group does.
	server.daemon.PublishAvatarUpdated(app.Avatar{Kind: app.AvatarSubjectKindChat, ID: "fam@g.us", LocalPath: "/cache/fam.jpg"})
	upd := c.expectUpsert(sub, "fam@g.us")
	if av := itemField(t, upd, "avatar_path"); av != "/cache/fam.jpg" {
		t.Fatalf("avatar_path = %q", av)
	}
}

// group_members: local roster at subscribe (all "member"), then the live card
// promotes roles and adds a member (upsert) — ordered superadmin, admin, member.
func TestGroupMembersTwoPhaseAndOrdering(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeGroups()
	actions.set("fam@g.us", app.GroupInfo{
		Members: []app.GroupMember{
			{JID: "a@s.whatsapp.net", DisplayName: "Alice"},
			{JID: "b@s.whatsapp.net", DisplayName: "Bob"},
		},
	})
	server.RegisterView("group_members", groupMembersView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"group_members","chat_id":"fam@g.us"}`)

	// Phase one: both members, role "member", ordered by name (Alice, Bob).
	a := c.expectUpsert(sub, "a@s.whatsapp.net")
	if r := itemField(t, a, "role"); r != "member" {
		t.Fatalf("alice role = %q, want member", r)
	}
	c.expectUpsert(sub, "b@s.whatsapp.net")
	c.expectReady(sub, true)

	// Live: Bob becomes superadmin, Carol joins as admin. Alice is unchanged
	// (still member, same sort) so the engine emits no event for her — only
	// Bob (role + sort changed) and Carol (new) upsert.
	server.daemon.PublishGroupInfoUpdated("fam@g.us", app.GroupInfo{
		Members: []app.GroupMember{
			{JID: "a@s.whatsapp.net", DisplayName: "Alice"},
			{JID: "b@s.whatsapp.net", DisplayName: "Bob", IsAdmin: true, IsSuperAdmin: true},
			{JID: "c@s.whatsapp.net", DisplayName: "Carol", IsAdmin: true},
		},
	})

	got := map[string]string{}
	for range 2 {
		msg := c.recvEvent()
		if msg["event"] != "upsert" {
			t.Fatalf("expected upsert, got %v", msg)
		}
		item := msg["item"].(map[string]any)
		got[item["id"].(string)] = item["role"].(string)
	}
	if got["b@s.whatsapp.net"] != "superadmin" {
		t.Fatalf("bob role = %q", got["b@s.whatsapp.net"])
	}
	if got["c@s.whatsapp.net"] != "admin" {
		t.Fatalf("carol role = %q", got["c@s.whatsapp.net"])
	}
}

// A member leaving (absent from the refreshed card) produces a remove.
func TestGroupMembersLeaveRemoves(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeGroups()
	actions.set("fam@g.us", app.GroupInfo{
		Members: []app.GroupMember{
			{JID: "a@s.whatsapp.net", DisplayName: "Alice"},
			{JID: "b@s.whatsapp.net", DisplayName: "Bob"},
		},
	})
	server.RegisterView("group_members", groupMembersView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"group_members","chat_id":"fam@g.us"}`)
	c.expectUpsert(sub, "a@s.whatsapp.net")
	c.expectUpsert(sub, "b@s.whatsapp.net")
	c.expectReady(sub, true)

	// Bob leaves.
	server.daemon.PublishGroupInfoUpdated("fam@g.us", app.GroupInfo{
		Members: []app.GroupMember{{JID: "a@s.whatsapp.net", DisplayName: "Alice"}},
	})
	c.expectRemove(sub, "b@s.whatsapp.net")
}

// A GroupInfoUpdated for a different group must not touch this subscription.
func TestGroupScopedToChat(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeGroups()
	actions.set("mine@g.us", app.GroupInfo{Subject: "Mine"})
	server.RegisterView("group", groupView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"group","chat_id":"mine@g.us"}`)
	c.expectUpsert(sub, "mine@g.us")
	c.expectReady(sub, true)

	server.daemon.PublishGroupInfoUpdated("other@g.us", app.GroupInfo{Subject: "Other"})
	server.daemon.PublishGroupInfoUpdated("mine@g.us", app.GroupInfo{Subject: "Mine!", MyRole: "member"})
	upd := c.expectUpsert(sub, "mine@g.us")
	if s := itemField(t, upd, "subject"); s != "Mine!" {
		t.Fatalf("subject = %q, want Mine!", s)
	}
}

func TestGroupRequiresChatID(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("group", groupView{daemon: server.daemon, actions: newFakeGroups()})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"group"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("missing chat_id error code = %q, want %q", code, CodeInvalidParams)
	}
}

func TestGroupInvalidChatID(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("group", groupView{daemon: server.daemon, actions: newFakeGroups()})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"group","chat_id":"nope@s.whatsapp.net"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("invalid chat_id error code = %q, want %q", code, CodeInvalidParams)
	}
}

func TestGroupMembersUnavailableWithoutActions(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("group_members", groupMembersView{daemon: server.daemon, actions: nil})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"group_members","chat_id":"fam@g.us"}}`)
	if code := errorCode(t, c.recv()); code != CodeInternal {
		t.Fatalf("nil-actions error code = %q, want %q", code, CodeInternal)
	}
}
