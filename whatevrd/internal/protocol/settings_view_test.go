package protocol

import (
	"context"
	"errors"
	"sync"
	"testing"

	"whatevrd/internal/app"
)

// fakeSettings is a mutable SettingsActions backend: tests set the privacy
// snapshot, preferences, and blocklist the accessors return (or mark the
// account logged out so the network-backed reads fail), standing in for
// *wa.Client.
type fakeSettings struct {
	mu        sync.Mutex
	privacy   app.PrivacySettings
	prefs     app.AppPreferences
	blocklist []app.BlockedContact
	loggedOut bool
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{prefs: app.DefaultAppPreferences()}
}

func (f *fakeSettings) GetPrivacySettings(context.Context) (app.PrivacySettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loggedOut {
		return app.PrivacySettings{}, errors.New("not connected")
	}
	return f.privacy, nil
}

func (f *fakeSettings) GetAppPreferences(context.Context) (app.AppPreferences, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prefs, nil
}

func (f *fakeSettings) GetBlocklist(context.Context) ([]app.BlockedContact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loggedOut {
		return nil, errors.New("not connected")
	}
	out := make([]app.BlockedContact, len(f.blocklist))
	copy(out, f.blocklist)
	return out, nil
}

func (f *fakeSettings) setPrivacy(p app.PrivacySettings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privacy = p
}

func (f *fakeSettings) setPrefs(p app.AppPreferences) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prefs = p
}

func (f *fakeSettings) setBlocklist(bc []app.BlockedContact) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocklist = bc
}

func (f *fakeSettings) setLoggedOut(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loggedOut = v
}

// privacy: local fill at subscribe, then a fresh snapshot event replaces it.
func TestPrivacyViewSnapshotUpdate(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeSettings()
	actions.setPrivacy(app.PrivacySettings{LastSeen: "contacts", ReadReceipts: true})
	server.RegisterView("privacy", privacyView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"privacy"}`)

	first := c.awaitInitialFill(sub, 1)[0]
	if got := first["item"].(map[string]any)["id"]; got != "self" {
		t.Fatalf("initial fill id = %v, want self", got)
	}
	if ls := itemField(t, first, "last_seen"); ls != "contacts" {
		t.Fatalf("last_seen = %q, want contacts", ls)
	}
	if rr := itemBool(t, first, "read_receipts"); !rr {
		t.Fatalf("read_receipts = false, want true")
	}

	// A change (here or on the phone) arrives as a full snapshot.
	server.daemon.PublishPrivacySettingsChanged(app.PrivacySettings{LastSeen: "nobody", Online: "match_last_seen", ReadReceipts: false})
	upd := c.expectUpsert(sub, "self")
	if ls := itemField(t, upd, "last_seen"); ls != "nobody" {
		t.Fatalf("last_seen after change = %q, want nobody", ls)
	}
	if rr := itemBool(t, upd, "read_receipts"); rr {
		t.Fatalf("read_receipts after change = true, want false")
	}
}

// privacy subscribed while logged out: empty until the connection comes up.
func TestPrivacyViewLoadsAfterLogin(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeSettings()
	actions.setLoggedOut(true)
	server.RegisterView("privacy", privacyView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"privacy"}`)
	c.expectReady(sub, true) // empty: no settings yet

	actions.setLoggedOut(false)
	actions.setPrivacy(app.PrivacySettings{LastSeen: "everyone"})
	server.daemon.SetStateDetail(app.StateOnline, "connected")

	first := c.expectUpsert(sub, "self")
	if ls := itemField(t, first, "last_seen"); ls != "everyone" {
		t.Fatalf("last_seen after login = %q, want everyone", ls)
	}
}

func TestPrivacyViewUnavailableWithoutActions(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("privacy", privacyView{daemon: server.daemon, actions: nil})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"privacy"}}`)
	if code := errorCode(t, c.recv()); code != CodeInternal {
		t.Fatalf("nil-actions privacy error code = %q, want %q", code, CodeInternal)
	}
}

// preferences: defaults at subscribe, then a change event re-reads them.
func TestPreferencesViewChangeRefetch(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeSettings()
	server.RegisterView("preferences", preferencesView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"preferences"}`)

	first := c.expectUpsert(sub, "self")
	if !itemBool(t, first, "notifications_enabled") {
		t.Fatalf("notifications_enabled default = false, want true")
	}
	if itemBool(t, first, "auto_download_photos") {
		t.Fatalf("auto_download_photos default = true, want false")
	}
	c.expectReady(sub, true)

	// A preferences change (via SetAppPreferences) fires the event; the view
	// re-reads the new values.
	actions.setPrefs(app.AppPreferences{NotificationsEnabled: false, AutoDownloadPhotos: true})
	server.daemon.PublishPreferencesChanged()
	upd := c.expectUpsert(sub, "self")
	if itemBool(t, upd, "notifications_enabled") {
		t.Fatalf("notifications_enabled after change = true, want false")
	}
	if !itemBool(t, upd, "auto_download_photos") {
		t.Fatalf("auto_download_photos after change = false, want true")
	}
}

func TestPreferencesViewUnavailableWithoutActions(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("preferences", preferencesView{daemon: server.daemon, actions: nil})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"preferences"}}`)
	if code := errorCode(t, c.recv()); code != CodeInternal {
		t.Fatalf("nil-actions preferences error code = %q, want %q", code, CodeInternal)
	}
}

// blocklist: sorted fill at subscribe, then a change adds/removes rows and an
// avatar refresh overlays in place.
func TestBlocklistViewLifecycle(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeSettings()
	actions.setBlocklist([]app.BlockedContact{
		{JID: "zed@s.whatsapp.net", DisplayName: "Zed", PhoneNumber: "+1 1"},
		{JID: "amy@s.whatsapp.net", DisplayName: "Amy", PhoneNumber: "+1 2"},
	})
	server.RegisterView("blocklist", blocklistView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"blocklist"}`)

	// Ordered by display name: Amy before Zed (one contiguous fill batch).
	fill := c.awaitInitialFill(sub, 2)
	if id := fill[0]["item"].(map[string]any)["id"]; id != "amy@s.whatsapp.net" {
		t.Fatalf("first fill id = %v, want amy", id)
	}
	if n := itemField(t, fill[0], "name"); n != "Amy" {
		t.Fatalf("first name = %q, want Amy", n)
	}
	if id := fill[1]["item"].(map[string]any)["id"]; id != "zed@s.whatsapp.net" {
		t.Fatalf("second fill id = %v, want zed", id)
	}

	// Unblock Zed, block Bob: Zed removed, Bob inserted (sorts after Amy).
	actions.setBlocklist([]app.BlockedContact{
		{JID: "amy@s.whatsapp.net", DisplayName: "Amy", PhoneNumber: "+1 2"},
		{JID: "bob@s.whatsapp.net", DisplayName: "Bob", PhoneNumber: "+1 3"},
	})
	server.daemon.PublishBlocklistChanged()
	// The engine emits the roster diff: a remove for Zed and an upsert for Bob.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		msg := c.recvEvent()
		switch msg["event"] {
		case "remove":
			got["remove:"+msg["id"].(string)] = true
		case "upsert":
			item := msg["item"].(map[string]any)
			got["upsert:"+item["id"].(string)] = true
		default:
			t.Fatalf("unexpected event: %v", msg)
		}
	}
	if !got["remove:zed@s.whatsapp.net"] || !got["upsert:bob@s.whatsapp.net"] {
		t.Fatalf("blocklist diff = %v, want Zed removed + Bob added", got)
	}

	// An avatar refresh for a held contact overlays the path.
	server.daemon.PublishAvatarUpdated(app.Avatar{
		Kind:      app.AvatarSubjectKindSender,
		ID:        "amy@s.whatsapp.net",
		LocalPath: "/cache/amy.jpg",
	})
	av := c.expectUpsert(sub, "amy@s.whatsapp.net")
	if p := itemField(t, av, "avatar_path"); p != "/cache/amy.jpg" {
		t.Fatalf("avatar_path after overlay = %q", p)
	}
}

// blocklist subscribed while logged out: empty until the connection comes up.
func TestBlocklistViewLoadsAfterLogin(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeSettings()
	actions.setLoggedOut(true)
	server.RegisterView("blocklist", blocklistView{daemon: server.daemon, actions: actions})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"blocklist"}`)
	c.expectReady(sub, true) // empty: not connected yet

	actions.setLoggedOut(false)
	actions.setBlocklist([]app.BlockedContact{{JID: "amy@s.whatsapp.net", DisplayName: "Amy"}})
	server.daemon.SetStateDetail(app.StateOnline, "connected")

	c.expectUpsert(sub, "amy@s.whatsapp.net")
}

func TestBlocklistViewUnavailableWithoutActions(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("blocklist", blocklistView{daemon: server.daemon, actions: nil})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"blocklist"}}`)
	if code := errorCode(t, c.recv()); code != CodeInternal {
		t.Fatalf("nil-actions blocklist error code = %q, want %q", code, CodeInternal)
	}
}
