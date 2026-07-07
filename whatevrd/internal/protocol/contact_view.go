package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"whatevrd/internal/app"
)

// ContactActions fetches the profile cards behind the `self` and `contact`
// views. *wa.Client implements it. Both calls return local data immediately and
// stream the network-fetched "about"/status text in afterwards as a
// DaemonEventContactInfoUpdated — the two-phase pattern from PROTOCOL.md, which
// here is just how a view works: the session overlays the enrichment onto the
// held card and re-upserts the whole item.
type ContactActions interface {
	GetContactInfo(ctx context.Context, jid string) (app.ContactInfo, error)
	SelfProfile(ctx context.Context) (app.ContactInfo, error)
}

// overlayContactStatus folds a DaemonEventContactInfoUpdated (which carries only
// {jid, status text}) onto a held card, reporting whether it changed anything.
func overlayContactStatus(info *app.ContactInfo, update app.ContactInfo) bool {
	if update.JID != info.JID || info.StatusText == update.StatusText {
		return false
	}
	info.StatusText = update.StatusText
	return true
}

// overlayContactAvatar folds a DaemonEventAvatarUpdated onto a held card when it
// is this card's sender avatar. The avatar subject id for the primary (PN) form
// equals the card's normalized jid; a LID-form refresh for the same person
// carries a different id and is not matched here — the primary refresh is the
// one the card renders.
func overlayContactAvatar(info *app.ContactInfo, av app.Avatar) bool {
	if av.Kind != app.AvatarSubjectKindSender || av.ID != info.JID {
		return false
	}
	if info.AvatarLocalPath == av.LocalPath {
		return false
	}
	info.AvatarLocalPath = av.LocalPath
	return true
}

// --- self ---------------------------------------------------------------

// selfView is the logged-in user's own profile card (jid, phone, push name,
// about, avatar). It is an object view: a single item under the id "self". The
// card re-fetches on DaemonEventSelfProfileChanged (name/about/photo changed
// here or on the phone) and, until first loaded, on the connection coming up
// after login; the async about text and avatar refresh overlay as they land.
type selfView struct {
	daemon  *app.Daemon
	actions ContactActions
}

type selfItem struct {
	ID         string `json:"id"`
	JID        string `json:"jid"`
	Phone      string `json:"phone,omitempty"`
	PushName   string `json:"push_name,omitempty"`
	About      string `json:"about,omitempty"`
	AvatarPath string `json:"avatar_path,omitempty"`
}

func (v selfView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "self view unavailable")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &selfSession{actions: v.actions, eventsCancel: cancel, done: make(chan struct{})}
	// Best-effort first load: SelfProfile errors while logged out, leaving the
	// view empty until login completes and a refetch fires.
	s.refetch()
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type selfSession struct {
	actions      ContactActions
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu     sync.Mutex
	loaded bool
	info   app.ContactInfo
}

func (s *selfSession) refetch() bool {
	info, err := s.actions.SelfProfile(context.Background())
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded && s.info == info {
		return false
	}
	s.loaded = true
	s.info = info
	return true
}

func (s *selfSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *selfSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

func (s *selfSession) apply(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync, app.DaemonEventSelfProfileChanged:
		return s.refetch()
	case app.DaemonEventConnectionChanged:
		// Recover the card once the connection comes up after a logged-out
		// subscribe; a genuine profile change later rides SelfProfileChanged.
		s.mu.Lock()
		loaded := s.loaded
		s.mu.Unlock()
		if !loaded {
			return s.refetch()
		}
		return false
	case app.DaemonEventContactInfoUpdated:
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.loaded {
			return false
		}
		return overlayContactStatus(&s.info, evt.ContactInfo)
	case app.DaemonEventAvatarUpdated:
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.loaded {
			return false
		}
		return overlayContactAvatar(&s.info, evt.Avatar)
	}
	return false
}

func (s *selfSession) Items(max int) []Item {
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
	item := selfItem{
		ID:         "self",
		JID:        s.info.JID,
		Phone:      s.info.PhoneNumber,
		PushName:   s.info.PushName,
		About:      s.info.StatusText,
		AvatarPath: s.info.AvatarLocalPath,
	}
	return []Item{{ID: "self", Sort: objectViewSort, Data: item}}
}

func (s *selfSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

// --- contact ------------------------------------------------------------

// contactView is a 1:1 contact card keyed by jid: saved/push/business name,
// phone, avatar, and the about text. Local fields fill the item at subscribe;
// the about text and avatar refresh overlay as they arrive. Object view, one
// item under the (normalized) jid.
type contactView struct {
	daemon  *app.Daemon
	actions ContactActions
}

type contactParams struct {
	JID string `json:"jid"`
}

type contactItem struct {
	ID           string `json:"id"`
	JID          string `json:"jid"`
	Phone        string `json:"phone,omitempty"`
	SavedName    string `json:"saved_name,omitempty"`
	PushName     string `json:"push_name,omitempty"`
	BusinessName string `json:"business_name,omitempty"`
	IsBusiness   bool   `json:"is_business,omitempty"`
	About        string `json:"about,omitempty"`
	AvatarPath   string `json:"avatar_path,omitempty"`
}

func (v contactView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p contactParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed contact params")
		}
	}
	if strings.TrimSpace(p.JID) == "" {
		return nil, nil, errorf(CodeInvalidParams, "contact params must carry a jid")
	}
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "contact view unavailable")
	}
	// A bad jid (malformed, or a group jid) is the only failure GetContactInfo
	// reports; a not-in-contacts user still returns a card from jid + phone.
	info, err := v.actions.GetContactInfo(context.Background(), p.JID)
	if err != nil {
		return nil, nil, errorf(CodeInvalidParams, "invalid contact jid: %v", err)
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &contactSession{actions: v.actions, jid: p.JID, info: info, eventsCancel: cancel, done: make(chan struct{})}
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type contactSession struct {
	actions      ContactActions
	jid          string
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu   sync.Mutex
	info app.ContactInfo
}

// refetch reloads the card from the actions seam, recovering the held state
// after a dropped-event gap (resync). The network "about"/avatar enrichment then
// re-streams through the usual overlay events; a transient failure keeps the
// current card.
func (s *contactSession) refetch() bool {
	info, err := s.actions.GetContactInfo(context.Background(), s.jid)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve the phase-two enrichment (about text, avatar) the reload does not
	// carry — GetContactInfo returns the phase-one card and re-schedules the async
	// status fetch, which re-streams the about via ContactInfoUpdated. Without this
	// a reconnect would blank the about until that fetch lands.
	if info.StatusText == "" {
		info.StatusText = s.info.StatusText
	}
	if info.AvatarLocalPath == "" {
		info.AvatarLocalPath = s.info.AvatarLocalPath
	}
	if s.info == info {
		return false
	}
	s.info = info
	return true
}

func (s *contactSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *contactSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

func (s *contactSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind == app.DaemonEventResync {
		return s.refetch()
	}
	if evt.Kind == app.DaemonEventConnectionChanged {
		// A contact card opened while logged out never got its network about/avatar
		// (GetContactInfo only schedules the status fetch when connected). Re-fetch
		// once the connection comes up so it fills, mirroring self's fill-after-login.
		if evt.State == app.StateOnline {
			return s.refetch()
		}
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch evt.Kind {
	case app.DaemonEventContactInfoUpdated:
		return overlayContactStatus(&s.info, evt.ContactInfo)
	case app.DaemonEventAvatarUpdated:
		return overlayContactAvatar(&s.info, evt.Avatar)
	}
	return false
}

func (s *contactSession) Items(max int) []Item {
	if max == 0 {
		max = 1
	}
	if max < 1 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := contactItem{
		ID:           s.info.JID,
		JID:          s.info.JID,
		Phone:        s.info.PhoneNumber,
		SavedName:    s.info.SavedName,
		PushName:     s.info.PushName,
		BusinessName: s.info.BusinessName,
		IsBusiness:   s.info.IsBusiness,
		About:        s.info.StatusText,
		AvatarPath:   s.info.AvatarLocalPath,
	}
	return []Item{{ID: s.info.JID, Sort: objectViewSort, Data: item}}
}

func (s *contactSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}
