package wa

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	"whatevrd/internal/notify"
)

const daemonConfigAppPreferencesKey = "app_preferences"

// ---- App preferences (daemon_config persisted, cached in memory) ----

// loadAppPreferences reads the persisted preferences into the in-memory cache,
// falling back to defaults when nothing has been saved yet. Best-effort: a read
// or decode error just leaves the defaults in place.
func (c *Client) loadAppPreferences(ctx context.Context) {
	prefs := app.DefaultAppPreferences()
	if raw, err := c.store.GetDaemonConfig(ctx, daemonConfigAppPreferencesKey); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &prefs)
	}
	c.appPrefs.Store(&prefs)
}

func (c *Client) appPreferences() app.AppPreferences {
	if p := c.appPrefs.Load(); p != nil {
		return *p
	}
	return app.DefaultAppPreferences()
}

// notificationOptions returns the formatting options for a notification plus
// whether notifications are enabled at all, both from cached preferences.
func (c *Client) notificationOptions() (notify.Options, bool) {
	p := c.appPreferences()
	return notify.Options{Preview: p.NotificationPreview, Sound: p.NotificationSound}, p.NotificationsEnabled
}

// GetAppPreferences returns the cached daemon-side preferences.
func (c *Client) GetAppPreferences(ctx context.Context) (app.AppPreferences, error) {
	return c.appPreferences(), nil
}

// SetAppPreferences persists and caches the daemon-side preferences wholesale.
func (c *Client) SetAppPreferences(ctx context.Context, prefs app.AppPreferences) (app.AppPreferences, error) {
	c.appPrefsMu.Lock()
	defer c.appPrefsMu.Unlock()
	return c.storeAppPreferencesLocked(ctx, prefs)
}

// UpdateAppPreferences applies a partial patch atomically: it reads the current
// preferences, runs apply, and persists the result under appPrefsMu, so two
// concurrent partial updates (the protocol's one partial-object command) cannot
// clobber each other's fields — the read-modify-write is indivisible. Rule 1:
// the daemon owns the merge, not the frontend.
func (c *Client) UpdateAppPreferences(ctx context.Context, apply func(*app.AppPreferences)) (app.AppPreferences, error) {
	c.appPrefsMu.Lock()
	defer c.appPrefsMu.Unlock()
	prefs := c.appPreferences()
	apply(&prefs)
	return c.storeAppPreferencesLocked(ctx, prefs)
}

// storeAppPreferencesLocked persists, caches, and announces prefs. The caller
// must hold appPrefsMu.
func (c *Client) storeAppPreferencesLocked(ctx context.Context, prefs app.AppPreferences) (app.AppPreferences, error) {
	data, err := json.Marshal(prefs)
	if err != nil {
		return app.AppPreferences{}, err
	}
	if err := c.store.SetDaemonConfig(ctx, daemonConfigAppPreferencesKey, string(data)); err != nil {
		return app.AppPreferences{}, err
	}
	c.appPrefs.Store(&prefs)
	c.daemon.PublishPreferencesChanged()
	return prefs, nil
}

// ---- Privacy settings (live WhatsApp connection) ----

func privacyToApp(s types.PrivacySettings) app.PrivacySettings {
	return app.PrivacySettings{
		LastSeen:     string(s.LastSeen),
		Online:       string(s.Online),
		ProfilePhoto: string(s.Profile),
		About:        string(s.Status),
		// WhatsApp only allows everyone/nobody for read receipts; undefined
		// (never changed) means the default-on "everyone".
		ReadReceipts: s.ReadReceipts != types.PrivacySettingNone,
		GroupAdd:     string(s.GroupAdd),
		CallAdd:      string(s.CallAdd),
	}
}

// GetPrivacySettings fetches the user's current WhatsApp privacy settings.
func (c *Client) GetPrivacySettings(ctx context.Context) (app.PrivacySettings, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return app.PrivacySettings{}, errNotConnected
	}
	settings, err := client.TryFetchPrivacySettings(ctx, false)
	if err != nil {
		return app.PrivacySettings{}, err
	}
	return privacyToApp(*settings), nil
}

// SetPrivacySetting updates one privacy category. category is the app-level key
// ("last_seen", "online", "profile_photo", "about", "read_receipts",
// "group_add", "call_add"); audience is the raw whatsmeow value for every
// category except read_receipts, which uses the readReceipts toggle.
func (c *Client) SetPrivacySetting(ctx context.Context, category, audience string, readReceipts bool) (app.PrivacySettings, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return app.PrivacySettings{}, errNotConnected
	}

	var (
		name  types.PrivacySettingType
		value types.PrivacySetting
	)
	switch category {
	case "last_seen":
		name, value = types.PrivacySettingTypeLastSeen, types.PrivacySetting(audience)
	case "online":
		name, value = types.PrivacySettingTypeOnline, types.PrivacySetting(audience)
	case "profile_photo":
		name, value = types.PrivacySettingTypeProfile, types.PrivacySetting(audience)
	case "about":
		name, value = types.PrivacySettingTypeStatus, types.PrivacySetting(audience)
	case "group_add":
		name, value = types.PrivacySettingTypeGroupAdd, types.PrivacySetting(audience)
	case "call_add":
		name, value = types.PrivacySettingTypeCallAdd, types.PrivacySetting(audience)
	case "read_receipts":
		name = types.PrivacySettingTypeReadReceipts
		if readReceipts {
			value = types.PrivacySettingAll
		} else {
			value = types.PrivacySettingNone
		}
	default:
		return app.PrivacySettings{}, errors.New("unknown privacy category")
	}
	if value == "" {
		return app.PrivacySettings{}, errors.New("invalid privacy value")
	}

	settings, err := client.SetPrivacySetting(ctx, name, value)
	if err != nil {
		return app.PrivacySettings{}, err
	}
	appSettings := privacyToApp(settings)
	c.daemon.PublishPrivacySettingsChanged(appSettings)
	return appSettings, nil
}

// ---- Blocklist (live WhatsApp connection) ----

// GetBlocklist returns the user's blocked contacts with display fields resolved.
func (c *Client) GetBlocklist(ctx context.Context) ([]app.BlockedContact, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil, errNotConnected
	}
	blocklist, err := client.GetBlocklist(ctx)
	if err != nil {
		return nil, err
	}
	return c.resolveBlockedContacts(ctx, blocklist.JIDs), nil
}

// UpdateBlocklist blocks or unblocks one contact and returns the updated list.
func (c *Client) UpdateBlocklist(ctx context.Context, jidStr string, block bool) ([]app.BlockedContact, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil, errNotConnected
	}
	jid, err := types.ParseJID(strings.TrimSpace(jidStr))
	if err != nil || jid.User == "" {
		return nil, errors.New("invalid jid")
	}
	action := waEvents.BlocklistChangeActionUnblock
	if block {
		action = waEvents.BlocklistChangeActionBlock
	}
	blocklist, err := client.UpdateBlocklist(ctx, jid.ToNonAD(), action)
	if err != nil {
		return nil, err
	}
	contacts := c.resolveBlockedContacts(ctx, blocklist.JIDs)
	c.daemon.PublishBlocklistChanged()
	return contacts, nil
}

func (c *Client) resolveBlockedContacts(ctx context.Context, jids []types.JID) []app.BlockedContact {
	out := make([]app.BlockedContact, 0, len(jids))
	for _, jid := range jids {
		canonical := bareAvatarJID(jid).String()
		name, avatar := c.participantDisplay(ctx, canonical)
		out = append(out, app.BlockedContact{
			JID:             jid.ToNonAD().String(),
			DisplayName:     name,
			PhoneNumber:     formatPhoneDisplayName(jid),
			AvatarLocalPath: avatar,
		})
	}
	return out
}

// ---- Profile ----

// SetProfileStatus updates the logged-in user's "About"/status text.
func (c *Client) SetProfileStatus(ctx context.Context, statusText string) error {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return errNotConnected
	}
	if err := client.SetStatusMessage(ctx, statusText); err != nil {
		return err
	}
	c.daemon.PublishSelfProfileChanged()
	if client.Store.ID != nil {
		c.daemon.PublishContactInfoUpdated(app.ContactInfo{JID: client.Store.ID.ToNonAD().String(), StatusText: strings.TrimSpace(statusText)})
	}
	return nil
}
