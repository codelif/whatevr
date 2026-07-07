package protocol

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"whatevrd/internal/app"
)

// SettingsActions is the client seam behind the `privacy`, `preferences`, and
// `blocklist` views: the account/app settings a subscription reads and re-reads
// on a daemon event. *wa.Client implements it; the fixture and store-free tests
// pass nil when the views they exercise do not touch it. GetPrivacySettings and
// GetBlocklist reach the network and fail while logged out (the view then opens
// empty and fills once the connection comes up); GetAppPreferences is
// daemon-persisted and always available.
type SettingsActions interface {
	GetPrivacySettings(ctx context.Context) (app.PrivacySettings, error)
	GetAppPreferences(ctx context.Context) (app.AppPreferences, error)
	GetBlocklist(ctx context.Context) ([]app.BlockedContact, error)
}

// --- privacy ------------------------------------------------------------

// privacyView is the account's WhatsApp privacy settings: an object view under
// the id "self". The live connection fills it at subscribe (empty until login);
// a change made here or on the phone arrives as DaemonEventPrivacySettingsChanged
// carrying a fresh snapshot, which replaces the held one wholesale.
type privacyView struct {
	daemon  *app.Daemon
	actions SettingsActions
}

type privacyItem struct {
	ID           string `json:"id"` // "self"
	LastSeen     string `json:"last_seen,omitempty"`
	Online       string `json:"online,omitempty"`
	ProfilePhoto string `json:"profile_photo,omitempty"`
	About        string `json:"about,omitempty"`
	ReadReceipts bool   `json:"read_receipts"`
	GroupAdd     string `json:"group_add,omitempty"`
	CallAdd      string `json:"call_add,omitempty"`
}

func (v privacyView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "privacy view unavailable")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &privacySession{actions: v.actions, eventsCancel: cancel, done: make(chan struct{})}
	// Best-effort first load: GetPrivacySettings errors while logged out, leaving
	// the view empty until the connection comes up (or a change snapshot lands).
	s.refetch()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type privacySession struct {
	actions      SettingsActions
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu       sync.Mutex
	loaded   bool
	settings app.PrivacySettings
}

func (s *privacySession) refetch() bool {
	settings, err := s.actions.GetPrivacySettings(context.Background())
	if err != nil {
		return false
	}
	return s.set(settings)
}

func (s *privacySession) set(settings app.PrivacySettings) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded && s.settings == settings {
		return false
	}
	s.loaded = true
	s.settings = settings
	return true
}

func (s *privacySession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *privacySession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.apply(evt) {
				invalidate()
			}
		}
	}
}

func (s *privacySession) apply(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventPrivacySettingsChanged:
		return s.set(evt.PrivacySettings)
	case app.DaemonEventConnectionChanged:
		// Fill after a logged-out subscribe once the connection comes up; a real
		// change later rides its own snapshot event.
		s.mu.Lock()
		loaded := s.loaded
		s.mu.Unlock()
		if !loaded {
			return s.refetch()
		}
	}
	return false
}

func (s *privacySession) Items(max int) []Item {
	if max == 0 {
		max = 1
	}
	if max < 1 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return nil
	}
	item := privacyItem{
		ID:           "self",
		LastSeen:     s.settings.LastSeen,
		Online:       s.settings.Online,
		ProfilePhoto: s.settings.ProfilePhoto,
		About:        s.settings.About,
		ReadReceipts: s.settings.ReadReceipts,
		GroupAdd:     s.settings.GroupAdd,
		CallAdd:      s.settings.CallAdd,
	}
	return []Item{{ID: "self", Sort: objectViewSort, Data: item}}
}

func (s *privacySession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

// --- preferences --------------------------------------------------------

// preferencesView is the daemon-persisted app preferences (notification gating,
// media auto-download): an object view under the id "self". These are not tied
// to the WhatsApp account, so they are always available (defaults before the
// user saves anything) and never open empty. A change via SetAppPreferences
// fires DaemonEventPreferencesChanged, off which the view re-reads them.
type preferencesView struct {
	daemon  *app.Daemon
	actions SettingsActions
}

type preferencesItem struct {
	ID                    string `json:"id"` // "self"
	NotificationsEnabled  bool   `json:"notifications_enabled"`
	NotificationSound     bool   `json:"notification_sound"`
	NotificationPreview   bool   `json:"notification_preview"`
	AutoDownloadPhotos    bool   `json:"auto_download_photos"`
	AutoDownloadVideos    bool   `json:"auto_download_videos"`
	AutoDownloadAudio     bool   `json:"auto_download_audio"`
	AutoDownloadDocuments bool   `json:"auto_download_documents"`
	AutoDownloadStickers  bool   `json:"auto_download_stickers"`
}

func (v preferencesView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "preferences view unavailable")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &preferencesSession{actions: v.actions, eventsCancel: cancel, done: make(chan struct{})}
	s.refetch()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type preferencesSession struct {
	actions      SettingsActions
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu    sync.Mutex
	prefs app.AppPreferences
}

func (s *preferencesSession) refetch() bool {
	prefs, err := s.actions.GetAppPreferences(context.Background())
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prefs == prefs {
		return false
	}
	s.prefs = prefs
	return true
}

func (s *preferencesSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *preferencesSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.apply(evt) {
				invalidate()
			}
		}
	}
}

func (s *preferencesSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind != app.DaemonEventPreferencesChanged {
		return false
	}
	return s.refetch()
}

func (s *preferencesSession) Items(max int) []Item {
	if max == 0 {
		max = 1
	}
	if max < 1 {
		return nil
	}
	s.mu.Lock()
	p := s.prefs
	s.mu.Unlock()
	item := preferencesItem{
		ID:                    "self",
		NotificationsEnabled:  p.NotificationsEnabled,
		NotificationSound:     p.NotificationSound,
		NotificationPreview:   p.NotificationPreview,
		AutoDownloadPhotos:    p.AutoDownloadPhotos,
		AutoDownloadVideos:    p.AutoDownloadVideos,
		AutoDownloadAudio:     p.AutoDownloadAudio,
		AutoDownloadDocuments: p.AutoDownloadDocuments,
		AutoDownloadStickers:  p.AutoDownloadStickers,
	}
	return []Item{{ID: "self", Sort: objectViewSort, Data: item}}
}

func (s *preferencesSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

// --- blocklist ----------------------------------------------------------

// blocklistView is the account's blocked contacts: an unwindowed collection,
// one item per blocked jid. The live connection fills it at subscribe (empty
// until login); DaemonEventBlocklistChanged (carrying no payload) triggers a
// whole re-read, and an avatar refresh for a held contact overlays in place.
// Rows are ordered by display name then jid — a user-facing settings list wants
// a stable, sensible order, and the daemon owns sort so the frontend only
// filters.
type blocklistView struct {
	daemon  *app.Daemon
	actions SettingsActions
}

type blocklistItem struct {
	ID         string `json:"id"` // jid
	JID        string `json:"jid"`
	Name       string `json:"name,omitempty"`
	Phone      string `json:"phone,omitempty"`
	AvatarPath string `json:"avatar_path,omitempty"`
}

func (v blocklistView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "blocklist view unavailable")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &blocklistSession{
		actions:      v.actions,
		eventsCancel: cancel,
		done:         make(chan struct{}),
		byJID:        map[string]app.BlockedContact{},
	}
	s.refetch()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type blocklistSession struct {
	actions      SettingsActions
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu     sync.Mutex
	loaded bool
	byJID  map[string]app.BlockedContact
}

// refetch replaces the whole held set from the live blocklist. It errors while
// logged out, in which case the set is left as-is (empty on the first attempt).
func (s *blocklistSession) refetch() bool {
	contacts, err := s.actions.GetBlocklist(context.Background())
	if err != nil {
		return false
	}
	next := make(map[string]app.BlockedContact, len(contacts))
	for _, bc := range contacts {
		next[bc.JID] = bc
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded && sameBlocklist(s.byJID, next) {
		return false
	}
	s.loaded = true
	s.byJID = next
	return true
}

func sameBlocklist(a, b map[string]app.BlockedContact) bool {
	if len(a) != len(b) {
		return false
	}
	for jid, bc := range a {
		if other, ok := b[jid]; !ok || other != bc {
			return false
		}
	}
	return true
}

func (s *blocklistSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *blocklistSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.apply(evt) {
				invalidate()
			}
		}
	}
}

func (s *blocklistSession) apply(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventBlocklistChanged:
		return s.refetch()
	case app.DaemonEventConnectionChanged:
		s.mu.Lock()
		loaded := s.loaded
		s.mu.Unlock()
		if !loaded {
			return s.refetch()
		}
	case app.DaemonEventAvatarUpdated:
		return s.overlayAvatar(evt.Avatar)
	}
	return false
}

// overlayAvatar folds an avatar refresh onto a held blocked contact when it is
// that contact's sender avatar. Like the contact card, a LID-form refresh for
// the same person carries a different id and is not matched — the primary
// refresh is the one already resolved into the row.
func (s *blocklistSession) overlayAvatar(av app.Avatar) bool {
	if av.Kind != app.AvatarSubjectKindSender {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bc, ok := s.byJID[av.ID]
	if !ok || bc.AvatarLocalPath == av.LocalPath {
		return false
	}
	bc.AvatarLocalPath = av.LocalPath
	s.byJID[av.ID] = bc
	return true
}

func (s *blocklistSession) Items(max int) []Item {
	s.mu.Lock()
	contacts := make([]app.BlockedContact, 0, len(s.byJID))
	for _, bc := range s.byJID {
		contacts = append(contacts, bc)
	}
	s.mu.Unlock()

	sort.Slice(contacts, func(i, j int) bool {
		return blocklistSortKey(contacts[i]) < blocklistSortKey(contacts[j])
	})
	if max > 0 && len(contacts) > max {
		contacts = contacts[:max]
	}

	items := make([]Item, 0, len(contacts))
	for _, bc := range contacts {
		items = append(items, Item{
			ID:   bc.JID,
			Sort: blocklistSortKey(bc),
			Data: blocklistItem{
				ID:         bc.JID,
				JID:        bc.JID,
				Name:       bc.DisplayName,
				Phone:      bc.PhoneNumber,
				AvatarPath: bc.AvatarLocalPath,
			},
		})
	}
	return items
}

// blocklistSortKey orders rows by lowercased display name then jid, so a rename
// (via a refreshed row) moves the item as an ordinary upsert and the jid tail
// keeps the key unique for nameless entries.
func blocklistSortKey(bc app.BlockedContact) string {
	return strings.ToLower(bc.DisplayName) + "\x1f" + bc.JID
}

func (s *blocklistSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}
