package protocol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

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

type sessionUpdateParams struct {
	Focused      *bool  `json:"focused"`
	ActiveChatID string `json:"active_chat_id"`
}

func (h commandHandlers) sessionUpdate(c *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sessionUpdateParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if p.Focused == nil {
		return nil, errorf(CodeInvalidParams, "focused is required")
	}
	c.ensureFrontendSession(h.actions)
	activeChatID := strings.TrimSpace(p.ActiveChatID)
	c.sessionMu.Lock()
	id := c.sessionID
	c.sessionFocused = *p.Focused
	c.sessionActiveChatID = activeChatID
	c.sessionUpdatedAt = time.Now()
	c.sessionMu.Unlock()
	h.actions.FrontendSessionStateChanged(id, *p.Focused, activeChatID)
	return nil, nil
}

func (h commandHandlers) daemonReconnect(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	if err := rejectNonEmptyParams(req.Params); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.Reconnect(context.Background()))
}

func (h commandHandlers) accountLogout(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	if err := rejectNonEmptyParams(req.Params); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.Logout(context.Background()))
}

type chatIDParams struct {
	ChatID string `json:"chat_id"`
}

func (p chatIDParams) valid() *Error {
	if strings.TrimSpace(p.ChatID) == "" {
		return errorf(CodeInvalidParams, "chat_id is required")
	}
	return nil
}

type chatMarkReadParams struct {
	ChatID        string `json:"chat_id"`
	UpToMessageID string `json:"up_to_message_id"`
}

func (h commandHandlers) chatMarkRead(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatMarkReadParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.UpToMessageID) == "" {
		return nil, errorf(CodeInvalidParams, "up_to_message_id is required")
	}
	_, err := h.actions.MarkChatReadUpTo(context.Background(), strings.TrimSpace(p.ChatID), strings.TrimSpace(p.UpToMessageID))
	return nil, mapCommandError(err)
}

type chatPinParams struct {
	ChatID string `json:"chat_id"`
	Pinned *bool  `json:"pinned"`
}

func (h commandHandlers) chatPin(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatPinParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Pinned == nil {
		return nil, errorf(CodeInvalidParams, "pinned is required")
	}
	_, err := h.actions.SetChatPinned(context.Background(), strings.TrimSpace(p.ChatID), *p.Pinned)
	return nil, mapCommandError(err)
}

type chatArchiveParams struct {
	ChatID   string `json:"chat_id"`
	Archived *bool  `json:"archived"`
}

func (h commandHandlers) chatArchive(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatArchiveParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Archived == nil {
		return nil, errorf(CodeInvalidParams, "archived is required")
	}
	_, err := h.actions.SetChatArchived(context.Background(), strings.TrimSpace(p.ChatID), *p.Archived)
	return nil, mapCommandError(err)
}

type chatMuteParams struct {
	ChatID       string `json:"chat_id"`
	Muted        *bool  `json:"muted"`
	DurationSecs int64  `json:"duration_secs"`
}

func (h commandHandlers) chatMute(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatMuteParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Muted == nil {
		return nil, errorf(CodeInvalidParams, "muted is required")
	}
	if p.DurationSecs < 0 {
		return nil, errorf(CodeInvalidParams, "duration_secs must be non-negative")
	}
	if p.DurationSecs > maxCommandDurationSecs {
		return nil, errorf(CodeInvalidParams, "duration_secs is too large")
	}
	_, err := h.actions.SetChatMuted(context.Background(), strings.TrimSpace(p.ChatID), *p.Muted, time.Duration(p.DurationSecs)*time.Second)
	return nil, mapCommandError(err)
}

type chatTypingParams struct {
	ChatID    string `json:"chat_id"`
	Composing *bool  `json:"composing"`
}

func (h commandHandlers) chatTyping(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatTypingParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Composing == nil {
		return nil, errorf(CodeInvalidParams, "composing is required")
	}
	return nil, mapCommandError(h.actions.SetChatPresence(context.Background(), strings.TrimSpace(p.ChatID), *p.Composing))
}

func (h commandHandlers) chatRequestOlder(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p chatIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	requested, err := h.actions.RequestOlderMessages(context.Background(), strings.TrimSpace(p.ChatID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"requested": requested}, nil
}

type ensureDirectParams struct {
	JID string `json:"jid"`
}

func (h commandHandlers) chatEnsureDirect(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p ensureDirectParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.JID) == "" {
		return nil, errorf(CodeInvalidParams, "jid is required")
	}
	chat, err := h.actions.EnsureDirectChat(context.Background(), strings.TrimSpace(p.JID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"chat_id": chat.ID}, nil
}

type sendTextParams struct {
	ChatID   string   `json:"chat_id"`
	Text     string   `json:"text"`
	ReplyTo  string   `json:"reply_to"`
	Mentions []string `json:"mentions"`
}

func (h commandHandlers) sendText(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendTextParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	// Validate against the trimmed text (reject whitespace-only) but send the
	// original: leading/trailing whitespace is user-authored content, not ours to
	// strip.
	if strings.TrimSpace(p.Text) == "" {
		return nil, errorf(CodeInvalidParams, "text is required")
	}
	if utf8.RuneCountInString(p.Text) > maxCommandTextRunes {
		return nil, errorf(CodeInvalidParams, "text must be <= %d characters", maxCommandTextRunes)
	}
	saved, err := h.actions.SendText(context.Background(), strings.TrimSpace(p.ChatID), p.Text, strings.TrimSpace(p.ReplyTo), trimStringSlice(p.Mentions))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type sendMediaParams struct {
	ChatID   string   `json:"chat_id"`
	Path     string   `json:"path"`
	Caption  string   `json:"caption"`
	ReplyTo  string   `json:"reply_to"`
	Mentions []string `json:"mentions"`
}

func (h commandHandlers) sendMedia(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendMediaParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	path := strings.TrimSpace(p.Path)
	if path == "" {
		return nil, errorf(CodeInvalidParams, "path is required")
	}
	// Caption is optional and user-authored; validate length but send it verbatim
	// (an intentional leading space or trailing newline is not ours to strip).
	if utf8.RuneCountInString(p.Caption) > maxCommandCaptionRunes {
		return nil, errorf(CodeInvalidParams, "caption must be <= %d characters", maxCommandCaptionRunes)
	}
	saved, err := h.actions.SendMediaWithMentions(context.Background(), strings.TrimSpace(p.ChatID), path, p.Caption, strings.TrimSpace(p.ReplyTo), trimStringSlice(p.Mentions))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type sendStickerParams struct {
	ChatID   string `json:"chat_id"`
	CacheKey string `json:"cache_key"`
	ReplyTo  string `json:"reply_to"`
}

func (h commandHandlers) sendSticker(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendStickerParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.CacheKey) == "" {
		return nil, errorf(CodeInvalidParams, "cache_key is required")
	}
	saved, err := h.actions.SendSticker(context.Background(), strings.TrimSpace(p.ChatID), strings.TrimSpace(p.CacheKey), strings.TrimSpace(p.ReplyTo))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type messageIDParams struct {
	MessageID string `json:"message_id"`
}

func (p messageIDParams) valid() *Error {
	if strings.TrimSpace(p.MessageID) == "" {
		return errorf(CodeInvalidParams, "message_id is required")
	}
	return nil
}

type messageReactParams struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

func (h commandHandlers) messageReact(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageReactParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	_, err := h.actions.SendReaction(context.Background(), strings.TrimSpace(p.MessageID), strings.TrimSpace(p.Emoji))
	return nil, mapCommandError(err)
}

type messageEditParams struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

func (h commandHandlers) messageEdit(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageEditParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	// As with send.text: validate trimmed, edit with the original text.
	if strings.TrimSpace(p.Text) == "" {
		return nil, errorf(CodeInvalidParams, "text is required")
	}
	if utf8.RuneCountInString(p.Text) > maxCommandTextRunes {
		return nil, errorf(CodeInvalidParams, "text must be <= %d characters", maxCommandTextRunes)
	}
	_, err := h.actions.EditMessage(context.Background(), strings.TrimSpace(p.MessageID), p.Text)
	return nil, mapCommandError(err)
}

func (h commandHandlers) messageRevoke(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	_, err := h.actions.RevokeMessage(context.Background(), strings.TrimSpace(p.MessageID))
	return nil, mapCommandError(err)
}

func (h commandHandlers) messageDelete(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	return nil, mapCommandError(h.actions.DeleteMessageForMe(context.Background(), strings.TrimSpace(p.MessageID)))
}

type messageStarParams struct {
	MessageID string `json:"message_id"`
	Starred   *bool  `json:"starred"`
}

func (h commandHandlers) messageStar(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageStarParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	if p.Starred == nil {
		return nil, errorf(CodeInvalidParams, "starred is required")
	}
	_, err := h.actions.SetMessageStarred(context.Background(), strings.TrimSpace(p.MessageID), *p.Starred)
	return nil, mapCommandError(err)
}

type messagePinParams struct {
	MessageID    string `json:"message_id"`
	Pinned       *bool  `json:"pinned"`
	DurationSecs int64  `json:"duration_secs"`
}

func (h commandHandlers) messagePin(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messagePinParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	if p.Pinned == nil {
		return nil, errorf(CodeInvalidParams, "pinned is required")
	}
	if p.DurationSecs < 0 || p.DurationSecs > math.MaxUint32 {
		return nil, errorf(CodeInvalidParams, "duration_secs must fit uint32")
	}
	_, err := h.actions.PinMessage(context.Background(), strings.TrimSpace(p.MessageID), *p.Pinned, uint32(p.DurationSecs))
	return nil, mapCommandError(err)
}

type messageForwardParams struct {
	MessageID string   `json:"message_id"`
	ChatIDs   []string `json:"chat_ids"`
}

func (h commandHandlers) messageForward(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageForwardParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	targets := uniqueTrimmedStrings(p.ChatIDs)
	if len(targets) == 0 {
		return nil, errorf(CodeInvalidParams, "at least one chat_id is required")
	}
	if len(targets) > maxCommandForwardTargets {
		return nil, errorf(CodeInvalidParams, "at most %d target chats per forward", maxCommandForwardTargets)
	}
	saved, err := h.actions.ForwardMessage(context.Background(), strings.TrimSpace(p.MessageID), targets)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	ids := make([]string, 0, len(saved))
	for _, result := range saved {
		ids = append(ids, result.Message.ID)
	}
	return map[string]any{"message_ids": ids}, nil
}

func (h commandHandlers) mediaDownload(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	// media.download is ack-then-lifecycle (PROTOCOL.md): the response is {} and
	// all progress/outcome is observable through the `transfers` view and the
	// message row (media.path on success, media.download_error on failure), which
	// DownloadMessageMedia already publishes. Run it in the background so the
	// command does not block on the full download; a detached context outlives the
	// request. The wa layer coalesces duplicate in-flight downloads of the same
	// message, so a repeat call is harmless.
	messageID := strings.TrimSpace(p.MessageID)
	go func() {
		if _, err := h.actions.DownloadMessageMedia(context.Background(), messageID); err != nil {
			log.Printf("protocol: media.download %s: %v", messageID, err)
		}
	}()
	return nil, nil
}

type fetchProfilePictureParams struct {
	JID string `json:"jid"`
}

func (h commandHandlers) mediaFetchProfilePicture(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p fetchProfilePictureParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.JID) == "" {
		return nil, errorf(CodeInvalidParams, "jid is required")
	}
	path, err := h.actions.FetchProfilePicture(context.Background(), strings.TrimSpace(p.JID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"path": path}, nil
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

func mapCommandError(err error) *Error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errorf(CodeNotFound, "%v", err)
	}
	if st, ok := grpcstatus.FromError(err); ok {
		message := st.Message()
		switch st.Code() {
		case codes.InvalidArgument:
			return errorf(CodeInvalidParams, "%s", message)
		case codes.NotFound:
			return errorf(CodeNotFound, "%s", message)
		case codes.FailedPrecondition:
			lower := strings.ToLower(message)
			if strings.Contains(lower, "expired") || strings.Contains(lower, "edit window") || strings.Contains(lower, "revoke window") {
				return errorf(CodeExpired, "%s", message)
			}
			if strings.Contains(lower, "logged in") || strings.Contains(lower, "login") {
				return errorf(CodeNotLoggedIn, "%s", message)
			}
			return errorf(CodeRejected, "%s", message)
		case codes.Unavailable:
			return errorf(CodeNotConnected, "%s", message)
		case codes.AlreadyExists:
			return errorf(CodeAlreadyExists, "%s", message)
		case codes.ResourceExhausted, codes.Aborted, codes.Unknown, codes.PermissionDenied:
			return errorf(CodeRejected, "%s", message)
		case codes.Internal:
			return errorf(CodeInternal, "%s", message)
		}
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "not connected") {
		return errorf(CodeNotConnected, "%v", err)
	}
	if strings.Contains(lower, "invalid jid") {
		return errorf(CodeInvalidParams, "%v", err)
	}
	return errorf(CodeInternal, "%v", err)
}
