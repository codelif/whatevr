package protocol

import (
	"context"
	"errors"
	"sync"
	"testing"

	"whatevrd/internal/app"
)

// fakeContacts is a mutable ContactActions backend: tests set the card a jid
// resolves to and the self profile SelfProfile returns (or mark it logged out),
// standing in for *wa.Client's store+network resolution.
type fakeContacts struct {
	mu      sync.Mutex
	byJID   map[string]app.ContactInfo
	self    app.ContactInfo
	selfErr bool
}

func newFakeContacts() *fakeContacts {
	return &fakeContacts{byJID: map[string]app.ContactInfo{}}
}

func (f *fakeContacts) GetContactInfo(_ context.Context, jid string) (app.ContactInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.byJID[jid]
	if !ok {
		// Mirror the real path: the only failure is a bad/group jid.
		return app.ContactInfo{}, errors.New("invalid user jid")
	}
	return info, nil
}

func (f *fakeContacts) SelfProfile(_ context.Context) (app.ContactInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.selfErr {
		return app.ContactInfo{}, errors.New("not logged in")
	}
	return f.self, nil
}

func (f *fakeContacts) setSelf(info app.ContactInfo, loggedOut bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.self = info
	f.selfErr = loggedOut
}

// itemField pulls a string field out of an upsert item.
func itemField(t *testing.T, msg map[string]any, key string) string {
	t.Helper()
	item, ok := msg["item"].(map[string]any)
	if !ok {
		t.Fatalf("upsert without an item: %v", msg)
	}
	s, _ := item[key].(string)
	return s
}

// contact: local card at subscribe, then the about text (a separate
// ContactInfoUpdated with only jid+status) and an avatar refresh overlay live.
func TestContactTwoPhaseOverlay(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeContacts()
	actions.byJID["aditi@s.whatsapp.net"] = app.ContactInfo{
		JID:         "aditi@s.whatsapp.net",
		PhoneNumber: "+91 88888 88888",
		SavedName:   "Aditi",
	}
	server.RegisterView("contact", contactView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"contact","jid":"aditi@s.whatsapp.net"}`)

	first := c.expectUpsert(sub, "aditi@s.whatsapp.net")
	if name := itemField(t, first, "saved_name"); name != "Aditi" {
		t.Fatalf("saved_name = %q, want Aditi", name)
	}
	if about := itemField(t, first, "about"); about != "" {
		t.Fatalf("about should be empty in phase one, got %q", about)
	}
	c.expectReady(sub, true)

	// Phase two: the about text lands as a jid+status-only event.
	server.daemon.PublishContactInfoUpdated(app.ContactInfo{
		JID:        "aditi@s.whatsapp.net",
		StatusText: "at the beach 🏖",
	})
	second := c.expectUpsert(sub, "aditi@s.whatsapp.net")
	if about := itemField(t, second, "about"); about != "at the beach 🏖" {
		t.Fatalf("about after overlay = %q", about)
	}
	// The saved name from phase one must survive the overlay.
	if name := itemField(t, second, "saved_name"); name != "Aditi" {
		t.Fatalf("saved_name lost after overlay: %q", name)
	}

	// Avatar refresh for this sender overlays the path.
	server.daemon.PublishAvatarUpdated(app.Avatar{
		Kind:      app.AvatarSubjectKindSender,
		ID:        "aditi@s.whatsapp.net",
		LocalPath: "/cache/aditi.jpg",
	})
	third := c.expectUpsert(sub, "aditi@s.whatsapp.net")
	if av := itemField(t, third, "avatar_path"); av != "/cache/aditi.jpg" {
		t.Fatalf("avatar_path after overlay = %q", av)
	}
}

// A ContactInfoUpdated / avatar refresh for a different jid must not touch this
// subscription.
func TestContactScopedToJID(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeContacts()
	actions.byJID["mine@s.whatsapp.net"] = app.ContactInfo{JID: "mine@s.whatsapp.net", SavedName: "Mine"}
	server.RegisterView("contact", contactView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"contact","jid":"mine@s.whatsapp.net"}`)
	c.expectUpsert(sub, "mine@s.whatsapp.net")
	c.expectReady(sub, true)

	// Another contact's status, then this one's: the first event the client sees
	// must be mine's upsert, proving the other was filtered.
	server.daemon.PublishContactInfoUpdated(app.ContactInfo{JID: "other@s.whatsapp.net", StatusText: "nope"})
	server.daemon.PublishAvatarUpdated(app.Avatar{Kind: app.AvatarSubjectKindSender, ID: "other@s.whatsapp.net", LocalPath: "/x"})
	server.daemon.PublishContactInfoUpdated(app.ContactInfo{JID: "mine@s.whatsapp.net", StatusText: "hi"})

	upd := c.expectUpsert(sub, "mine@s.whatsapp.net")
	if about := itemField(t, upd, "about"); about != "hi" {
		t.Fatalf("about = %q, want hi", about)
	}
}

func TestContactRequiresJID(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("contact", contactView{daemon: server.daemon, actions: newFakeContacts()})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"contact"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("missing jid error code = %q, want %q", code, CodeInvalidParams)
	}
}

func TestContactInvalidJID(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("contact", contactView{daemon: server.daemon, actions: newFakeContacts()})

	c := dialTest(t, socketPath)
	c.hello()
	// Not in the fake's map: GetContactInfo errors, mapped to invalid_params.
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"contact","jid":"12345@g.us"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("invalid jid error code = %q, want %q", code, CodeInvalidParams)
	}
}

// self: initial profile, then the about text overlays, then a genuine profile
// change (SelfProfileChanged) re-fetches the whole card.
func TestSelfProfileLifecycle(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeContacts()
	actions.setSelf(app.ContactInfo{
		JID:         "me@s.whatsapp.net",
		PhoneNumber: "+91 99999 99999",
		PushName:    "Harsh",
	}, false)
	server.RegisterView("self", selfView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"self"}`)

	first := c.expectUpsert(sub, "self")
	if name := itemField(t, first, "push_name"); name != "Harsh" {
		t.Fatalf("push_name = %q, want Harsh", name)
	}
	c.expectReady(sub, true)

	// About text streams in as a ContactInfoUpdated for the self jid.
	server.daemon.PublishContactInfoUpdated(app.ContactInfo{JID: "me@s.whatsapp.net", StatusText: "busy"})
	upd := c.expectUpsert(sub, "self")
	if about := itemField(t, upd, "about"); about != "busy" {
		t.Fatalf("about = %q, want busy", about)
	}

	// A genuine change (e.g. set name/about on the phone) refetches the card.
	actions.setSelf(app.ContactInfo{JID: "me@s.whatsapp.net", PhoneNumber: "+91 99999 99999", PushName: "Harsh S"}, false)
	server.daemon.PublishSelfProfileChanged()
	refetched := c.expectUpsert(sub, "self")
	if name := itemField(t, refetched, "push_name"); name != "Harsh S" {
		t.Fatalf("push_name after refetch = %q, want Harsh S", name)
	}
}

// self subscribed while logged out: empty until the connection comes up, then
// the card fills.
func TestSelfLoadsAfterLogin(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeContacts()
	actions.setSelf(app.ContactInfo{}, true) // logged out
	server.RegisterView("self", selfView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"self"}`)
	c.expectReady(sub, true) // empty view: no profile yet

	// Login completes: profile becomes available and the connection comes up.
	actions.setSelf(app.ContactInfo{JID: "me@s.whatsapp.net", PushName: "Harsh"}, false)
	server.daemon.SetStateDetail(app.StateOnline, "connected")

	first := c.expectUpsert(sub, "self")
	if name := itemField(t, first, "push_name"); name != "Harsh" {
		t.Fatalf("push_name after login = %q, want Harsh", name)
	}
}

func TestSelfViewUnavailableWithoutActions(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("self", selfView{daemon: server.daemon, actions: nil})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"self"}}`)
	if code := errorCode(t, c.recv()); code != CodeInternal {
		t.Fatalf("nil-actions self error code = %q, want %q", code, CodeInternal)
	}
}
