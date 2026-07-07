package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"whatevrd/internal/app"
)

// GroupActions fetches the group card behind the `group` and `group_members`
// views. *wa.Client implements it. GetGroupInfo returns the stored members
// immediately and streams the live subject/description/roles/owner/flags in
// afterwards as a DaemonEventGroupInfoUpdated carrying the whole enriched card —
// the two-phase pattern from PROTOCOL.md; both views replace their held card
// with the enriched one when it lands.
type GroupActions interface {
	GetGroupInfo(ctx context.Context, chatID string) (app.GroupInfo, error)
}

// --- group --------------------------------------------------------------

// groupView is a group chat's card (subject, description, avatar, created,
// owner, member_count, my_role, announce/locked) keyed by chat_id. It is an
// object view: a single item, deliberately without the member array — the chat
// header and card chrome need only this; the roster is the group_members view.
type groupView struct {
	daemon  *app.Daemon
	actions GroupActions
}

type groupParams struct {
	ChatID string `json:"chat_id"`
}

type groupItem struct {
	ID          string `json:"id"`
	Subject     string `json:"subject,omitempty"`
	Description string `json:"description,omitempty"`
	AvatarPath  string `json:"avatar_path,omitempty"`
	CreatedUnix int64  `json:"created_unix,omitempty"`
	Owner       string `json:"owner,omitempty"`
	MemberCount int    `json:"member_count"`
	MyRole      string `json:"my_role,omitempty"`
	Announce    bool   `json:"announce,omitempty"`
	Locked      bool   `json:"locked,omitempty"`
}

func (v groupView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	chatID, verr := groupChatID(params, "group")
	if verr != nil {
		return nil, nil, verr
	}
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "group view unavailable")
	}
	info, err := v.actions.GetGroupInfo(context.Background(), chatID)
	if err != nil {
		return nil, nil, errorf(CodeInvalidParams, "invalid group chat_id: %v", err)
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &groupSession{actions: v.actions, chatID: chatID, info: info, eventsCancel: cancel, done: make(chan struct{})}
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type groupSession struct {
	actions      GroupActions
	chatID       string
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu   sync.Mutex
	info app.GroupInfo
}

// refetch reloads the group card after a dropped-event gap (resync). It returns
// the phase-one card and spawns the live enrichment, which re-streams through
// DaemonEventGroupInfoUpdated — the same two-phase shape as Open.
func (s *groupSession) refetch() bool {
	info, err := s.actions.GetGroupInfo(context.Background(), s.chatID)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve a resolved avatar the reload doesn't carry (cold-cache GetGroupInfo
	// returns an empty avatar); the same protection the live-update path uses.
	if info.AvatarLocalPath == "" {
		info.AvatarLocalPath = s.info.AvatarLocalPath
	}
	s.info = info
	return true
}

func (s *groupSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *groupSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

func (s *groupSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind == app.DaemonEventResync {
		return s.refetch()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch evt.Kind {
	case app.DaemonEventGroupInfoUpdated:
		if evt.GroupChatID != s.chatID {
			return false
		}
		// The live fetch delivers the whole enriched card; replace wholesale, but
		// preserve an avatar already resolved onto the card when the incoming card
		// carries none. refreshGroupInfoLive captures the avatar at phase one, so a
		// card enriched after a cold-cache open would otherwise wipe the avatar the
		// separate AvatarUpdated overlay just delivered — leaving the header with no
		// avatar for the rest of the session (no further GroupInfoUpdated arrives).
		info := evt.GroupInfo
		if info.AvatarLocalPath == "" {
			info.AvatarLocalPath = s.info.AvatarLocalPath
		}
		s.info = info
		return true
	case app.DaemonEventAvatarUpdated:
		if evt.Avatar.Kind != app.AvatarSubjectKindChat || evt.Avatar.ID != s.chatID {
			return false
		}
		if s.info.AvatarLocalPath == evt.Avatar.LocalPath {
			return false
		}
		s.info.AvatarLocalPath = evt.Avatar.LocalPath
		return true
	}
	return false
}

func (s *groupSession) Items(max int) []Item {
	if max == 0 {
		max = 1
	}
	if max < 1 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := groupItem{
		ID:          s.chatID,
		Subject:     s.info.Subject,
		Description: s.info.Description,
		AvatarPath:  s.info.AvatarLocalPath,
		CreatedUnix: s.info.CreatedUnix,
		Owner:       s.info.OwnerJID,
		MemberCount: len(s.info.Members),
		MyRole:      s.info.MyRole,
		Announce:    s.info.IsAnnounce,
		Locked:      s.info.IsLocked,
	}
	return []Item{{ID: s.chatID, Sort: objectViewSort, Data: item}}
}

func (s *groupSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

// --- group_members ------------------------------------------------------

// groupMembersView is a group's roster keyed by chat_id: one item per member
// (jid, display name, phone, avatar, role). Joins/leaves/promotions arrive as
// the enriched card replaces the held member set — the engine diffs it into
// upserts and removes. Rows are ordered role-then-name so the info dialog reads
// naturally; member search is presentation-side filtering over these rows.
type groupMembersView struct {
	daemon  *app.Daemon
	actions GroupActions
}

type groupMemberItem struct {
	ID          string `json:"id"`
	JID         string `json:"jid"`
	DisplayName string `json:"display_name,omitempty"`
	Phone       string `json:"phone,omitempty"`
	AvatarPath  string `json:"avatar_path,omitempty"`
	Role        string `json:"role"`
}

func (v groupMembersView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	chatID, verr := groupChatID(params, "group_members")
	if verr != nil {
		return nil, nil, verr
	}
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "group_members view unavailable")
	}
	info, err := v.actions.GetGroupInfo(context.Background(), chatID)
	if err != nil {
		return nil, nil, errorf(CodeInvalidParams, "invalid group chat_id: %v", err)
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &groupMembersSession{actions: v.actions, chatID: chatID, members: info.Members, eventsCancel: cancel, done: make(chan struct{})}
	s.drainInitial(events)
	go s.run(events, invalidate)
	return s, nil, nil
}

type groupMembersSession struct {
	actions      GroupActions
	chatID       string
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once

	mu      sync.Mutex
	members []app.GroupMember
}

// refetch reloads the roster after a dropped-event gap (resync); the live
// enrichment re-streams through DaemonEventGroupInfoUpdated as after Open.
func (s *groupMembersSession) refetch() bool {
	info, err := s.actions.GetGroupInfo(context.Background(), s.chatID)
	if err != nil {
		return false
	}
	s.mu.Lock()
	s.members = info.Members
	s.mu.Unlock()
	return true
}

func (s *groupMembersSession) drainInitial(events <-chan app.DaemonEvent) {
	for {
		select {
		case evt := <-events:
			s.apply(evt)
		default:
			return
		}
	}
}

func (s *groupMembersSession) run(events <-chan app.DaemonEvent, invalidate func()) {
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

func (s *groupMembersSession) apply(evt app.DaemonEvent) bool {
	if evt.Kind == app.DaemonEventResync {
		return s.refetch()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch evt.Kind {
	case app.DaemonEventGroupInfoUpdated:
		if evt.GroupChatID != s.chatID {
			return false
		}
		s.members = evt.GroupInfo.Members
		return true
	case app.DaemonEventAvatarUpdated:
		if evt.Avatar.Kind != app.AvatarSubjectKindSender {
			return false
		}
		for i := range s.members {
			if s.members[i].JID == evt.Avatar.ID {
				if s.members[i].AvatarLocalPath == evt.Avatar.LocalPath {
					return false
				}
				s.members[i].AvatarLocalPath = evt.Avatar.LocalPath
				return true
			}
		}
		return false
	}
	return false
}

// Items renders one item per member. The view is unwindowed (the info dialog
// wants the whole roster), so max is ignored. The sort key orders superadmins,
// then admins, then members, each group alphabetized by display name, with the
// jid as a final tiebreaker for stability.
func (s *groupMembersSession) Items(_ int) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Item, 0, len(s.members))
	for _, m := range s.members {
		role := app.GroupRoleString(m.IsAdmin, m.IsSuperAdmin)
		items = append(items, Item{
			ID:   m.JID,
			Sort: memberSortKey(role, m.DisplayName, m.JID),
			Data: groupMemberItem{
				ID:          m.JID,
				JID:         m.JID,
				DisplayName: m.DisplayName,
				Phone:       m.PhoneNumber,
				AvatarPath:  m.AvatarLocalPath,
				Role:        role,
			},
		})
	}
	return items
}

func (s *groupMembersSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

// memberSortKey ranks superadmin(0) < admin(1) < member(2), then display name
// (lowercased), then jid. \x1f (unit separator) delimits fields so a name can
// never bleed into the jid comparison.
func memberSortKey(role, name, jid string) string {
	rank := 2
	switch role {
	case "superadmin":
		rank = 0
	case "admin":
		rank = 1
	}
	return fmt.Sprintf("%d\x1f%s\x1f%s", rank, strings.ToLower(name), jid)
}

// groupChatID pulls and validates the chat_id shared by both group views.
func groupChatID(params json.RawMessage, view string) (string, *Error) {
	var p groupParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return "", errorf(CodeInvalidParams, "malformed %s params", view)
		}
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return "", errorf(CodeInvalidParams, "%s params must carry a chat_id", view)
	}
	return p.ChatID, nil
}
