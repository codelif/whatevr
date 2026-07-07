package protocol

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

const (
	maxCommandTextRunes      = 65536
	maxCommandCaptionRunes   = 1024
	maxCommandForwardTargets = 5
	// maxCommandDurationSecs bounds any seconds→time.Duration conversion so the
	// nanosecond multiply cannot overflow int64 (and rejects absurd values well
	// before that): ~100 years is far beyond any real mute/pin horizon.
	maxCommandDurationSecs = 100 * 365 * 24 * 60 * 60
	// groupJIDSuffix is the WhatsApp server suffix for group jids; contact-only
	// commands reject any jid carrying it (the protocol layer works with string
	// jids and must not import wa's jid types).
	groupJIDSuffix = "@g.us"
)

// CommandActions is the daemon/WA seam used by protocol commands. *wa.Client
// implements it; tests and the conformance fixture can provide a small fake.
type CommandActions interface {
	FrontendSessionStarted(string)
	FrontendSessionEnded(string)
	FrontendSessionStateChanged(string, bool, string)

	Reconnect(context.Context) error
	Logout(context.Context) error

	MarkChatReadUpTo(context.Context, string, string) (appstore.Chat, error)
	SetChatPinned(context.Context, string, bool) (appstore.Chat, error)
	SetChatArchived(context.Context, string, bool) (appstore.Chat, error)
	SetChatMuted(context.Context, string, bool, time.Duration) (appstore.Chat, error)
	SetChatPresence(context.Context, string, bool) error
	RequestOlderMessages(context.Context, string) (bool, error)
	EnsureDirectChat(context.Context, string) (appstore.Chat, error)

	SendText(context.Context, string, string, string, []string) (appstore.SavedTextMessage, error)
	SendMediaWithMentions(context.Context, string, string, string, string, []string) (appstore.SavedTextMessage, error)
	SendSticker(context.Context, string, string, string) (appstore.SavedTextMessage, error)
	SendReaction(context.Context, string, string) (appstore.Message, error)
	EditMessage(context.Context, string, string) (appstore.Message, error)
	RevokeMessage(context.Context, string) (appstore.Message, error)
	DeleteMessageForMe(context.Context, string) error
	SetMessageStarred(context.Context, string, bool) (appstore.Message, error)
	PinMessage(context.Context, string, bool, uint32) (appstore.Message, error)
	ForwardMessage(context.Context, string, []string) ([]appstore.SavedTextMessage, error)
	DownloadMessageMedia(context.Context, string) (appstore.Message, error)
	FetchProfilePicture(context.Context, string) (string, error)

	SetPrivacySetting(context.Context, string, string, bool) (app.PrivacySettings, error)
	UpdateAppPreferences(context.Context, func(*app.AppPreferences)) (app.AppPreferences, error)
	SetProfileStatus(context.Context, string) error
	UpdateBlocklist(context.Context, string, bool) ([]app.BlockedContact, error)
	SetStickerFavorite(context.Context, string, string, bool) (appstore.Sticker, error)
	DownloadSticker(context.Context, string) (appstore.Sticker, error)
	SetStickerPackInstalled(context.Context, string, bool) (appstore.StickerPack, error)
	SearchChats(context.Context, string, int) ([]appstore.Chat, error)
	SearchMessages(context.Context, string, string, int, string) ([]appstore.MessageSearchResult, error)
	CheckPhoneOnWhatsApp(context.Context, string) (app.PhoneCheck, error)
}

// RegisterDaemonCommands registers the command surface from PROTOCOL.md.
func RegisterDaemonCommands(s *Server, actions CommandActions) {
	s.commandActions = actions
	cmd := commandHandlers{actions: actions}
	s.RegisterCommand("session.update", cmd.sessionUpdate)
	s.RegisterCommand("daemon.reconnect", cmd.daemonReconnect)
	s.RegisterCommand("account.logout", cmd.accountLogout)
	s.RegisterCommand("chat.mark_read", cmd.chatMarkRead)
	s.RegisterCommand("chat.pin", cmd.chatPin)
	s.RegisterCommand("chat.archive", cmd.chatArchive)
	s.RegisterCommand("chat.mute", cmd.chatMute)
	s.RegisterCommand("chat.typing", cmd.chatTyping)
	s.RegisterCommand("chat.request_older", cmd.chatRequestOlder)
	s.RegisterCommand("chat.ensure_direct", cmd.chatEnsureDirect)
	s.RegisterCommand("send.text", cmd.sendText)
	s.RegisterCommand("send.media", cmd.sendMedia)
	s.RegisterCommand("send.sticker", cmd.sendSticker)
	s.RegisterCommand("message.react", cmd.messageReact)
	s.RegisterCommand("message.edit", cmd.messageEdit)
	s.RegisterCommand("message.revoke", cmd.messageRevoke)
	s.RegisterCommand("message.delete", cmd.messageDelete)
	s.RegisterCommand("message.star", cmd.messageStar)
	s.RegisterCommand("message.pin", cmd.messagePin)
	s.RegisterCommand("message.forward", cmd.messageForward)
	s.RegisterCommand("media.download", cmd.mediaDownload)
	s.RegisterCommand("media.fetch_profile_picture", cmd.mediaFetchProfilePicture)
	// Phase C3 settings/contact/sticker commands and transient queries.
	s.RegisterCommand("privacy.set", cmd.privacySet)
	s.RegisterCommand("preferences.set", cmd.preferencesSet)
	s.RegisterCommand("self.set_about", cmd.selfSetAbout)
	s.RegisterCommand("contact.block", cmd.contactBlock)
	s.RegisterCommand("sticker.favorite", cmd.stickerFavorite)
	s.RegisterCommand("sticker.download", cmd.stickerDownload)
	s.RegisterCommand("sticker_pack.install", cmd.stickerPackInstall)
	s.RegisterCommand("search.chats", cmd.searchChats)
	s.RegisterCommand("search.messages", cmd.searchMessages)
	s.RegisterCommand("contacts.check_phone", cmd.contactsCheckPhone)
}

// RegisterCommand makes a command request method available. Registration is
// mutex-guarded, so it is safe even if the server is already accepting
// connections; production wiring still registers everything before Serve so no
// client can observe a half-registered surface.
func (s *Server) RegisterCommand(name string, h handlerFunc) {
	s.handlerMu.Lock()
	defer s.handlerMu.Unlock()
	s.handlers[name] = h
}

// handler looks up a method's handler under the registry lock.
func (s *Server) handler(name string) (handlerFunc, bool) {
	s.handlerMu.RLock()
	defer s.handlerMu.RUnlock()
	h, ok := s.handlers[name]
	return h, ok
}

type commandHandlers struct {
	actions CommandActions
}

func (h commandHandlers) requireActions() *Error {
	if h.actions == nil {
		return errorf(CodeInternal, "command actions are not available")
	}
	return nil
}

func (c *conn) ensureFrontendSession(actions CommandActions) {
	c.sessionMu.Lock()
	if c.sessionActive {
		c.sessionMu.Unlock()
		return
	}
	if c.sessionID == "" {
		c.sessionID = "protocol-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	id := c.sessionID
	c.sessionActive = true
	c.sessionUpdatedAt = time.Now()
	c.sessionMu.Unlock()
	actions.FrontendSessionStarted(id)
}

func decodeParams(raw json.RawMessage, out any) *Error {
	if len(raw) == 0 {
		return errorf(CodeInvalidParams, "params are required")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errorf(CodeInvalidParams, "malformed params")
	}
	return nil
}

func rejectNonEmptyParams(raw json.RawMessage) *Error {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return errorf(CodeInvalidParams, "malformed params")
	}
	if len(obj) != 0 {
		return errorf(CodeInvalidParams, "params must be empty")
	}
	return nil
}

func trimStringSlice(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func uniqueTrimmedStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
