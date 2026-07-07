package protocol

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

type fakeCommandActions struct {
	mu sync.Mutex

	started []string
	ended   []string
	state   []frontendStateCall

	markReadChat string
	markReadUpTo string
	pinnedChat   string
	pinned       bool
	archivedChat string
	archived     bool
	mutedChat    string
	muted        bool
	muteDuration time.Duration
	typingChat   string
	composing    bool
	requested    bool
	ensuredJID   string

	sendTextChat      string
	sendTextText      string
	sendTextReply     string
	sendTextMentions  []string
	sendMediaChat     string
	sendMediaPath     string
	sendMediaCaption  string
	sendMediaReply    string
	sendMediaMentions []string
	sendStickerChat   string
	sendStickerKey    string
	sendStickerReply  string
	reactMessage      string
	reactEmoji        string
	editMessage       string
	editText          string
	revokeMessage     string
	deleteMessage     string
	starMessage       string
	starred           bool
	pinMessage        string
	messagePinned     bool
	pinDuration       uint32
	forwardMessage    string
	forwardChats      []string
	downloadMessage   string
	fetchJID          string

	privacyCategory  string
	privacyAudience  string
	privacyRead      bool
	prefs            app.AppPreferences
	setPrefs         app.AppPreferences
	profileStatus    string
	blockJID         string
	blocked          bool
	favoriteKey      string
	favoriteMessage  string
	favorite         bool
	downloadSticker  string
	installPackID    string
	installed        bool
	searchChatsQuery string
	searchChatsLimit int
	searchMsgQuery   string
	searchMsgChat    string
	searchMsgLimit   int
	searchMsgBefore  string
	checkPhone       string

	err error
}

type frontendStateCall struct {
	sessionID    string
	focused      bool
	activeChatID string
}

func (f *fakeCommandActions) FrontendSessionStarted(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, id)
}
func (f *fakeCommandActions) FrontendSessionEnded(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, id)
}
func (f *fakeCommandActions) FrontendSessionStateChanged(id string, focused bool, activeChatID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = append(f.state, frontendStateCall{id, focused, activeChatID})
}
func (f *fakeCommandActions) Reconnect(context.Context) error { return f.err }
func (f *fakeCommandActions) Logout(context.Context) error    { return f.err }
func (f *fakeCommandActions) MarkChatReadUpTo(_ context.Context, chatID, upTo string) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markReadChat, f.markReadUpTo = chatID, upTo
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatPinned(_ context.Context, chatID string, pinned bool) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinnedChat, f.pinned = chatID, pinned
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatArchived(_ context.Context, chatID string, archived bool) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archivedChat, f.archived = chatID, archived
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatMuted(_ context.Context, chatID string, muted bool, d time.Duration) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutedChat, f.muted, f.muteDuration = chatID, muted, d
	return appstore.Chat{ID: chatID}, f.err
}
func (f *fakeCommandActions) SetChatPresence(_ context.Context, chatID string, composing bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typingChat, f.composing = chatID, composing
	return f.err
}
func (f *fakeCommandActions) RequestOlderMessages(_ context.Context, chatID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = true
	return true, f.err
}
func (f *fakeCommandActions) EnsureDirectChat(_ context.Context, jid string) (appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensuredJID = jid
	return appstore.Chat{ID: "chat-" + jid}, f.err
}
func (f *fakeCommandActions) SendText(_ context.Context, chatID, text, reply string, mentions []string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendTextChat, f.sendTextText, f.sendTextReply, f.sendTextMentions = chatID, text, reply, append([]string(nil), mentions...)
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "text-id", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendMediaWithMentions(_ context.Context, chatID, path, caption, reply string, mentions []string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendMediaChat, f.sendMediaPath, f.sendMediaCaption, f.sendMediaReply, f.sendMediaMentions = chatID, path, caption, reply, append([]string(nil), mentions...)
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "media-id", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendSticker(_ context.Context, chatID, cacheKey, reply string) (appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendStickerChat, f.sendStickerKey, f.sendStickerReply = chatID, cacheKey, reply
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "sticker-id", ChatID: chatID}}, f.err
}
func (f *fakeCommandActions) SendReaction(_ context.Context, messageID, emoji string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactMessage, f.reactEmoji = messageID, emoji
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) EditMessage(_ context.Context, messageID, text string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editMessage, f.editText = messageID, text
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) RevokeMessage(_ context.Context, messageID string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeMessage = messageID
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) DeleteMessageForMe(_ context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteMessage = messageID
	return f.err
}
func (f *fakeCommandActions) SetMessageStarred(_ context.Context, messageID string, starred bool) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starMessage, f.starred = messageID, starred
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) PinMessage(_ context.Context, messageID string, pinned bool, durationSecs uint32) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinMessage, f.messagePinned, f.pinDuration = messageID, pinned, durationSecs
	return appstore.Message{ID: messageID}, f.err
}
func (f *fakeCommandActions) ForwardMessage(_ context.Context, messageID string, chatIDs []string) ([]appstore.SavedTextMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forwardMessage, f.forwardChats = messageID, append([]string(nil), chatIDs...)
	out := make([]appstore.SavedTextMessage, 0, len(chatIDs))
	for i, chatID := range chatIDs {
		out = append(out, appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":f" + string(rune('0'+i)), ChatID: chatID}})
	}
	return out, f.err
}
func (f *fakeCommandActions) DownloadMessageMedia(_ context.Context, messageID string) (appstore.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadMessage = messageID
	return appstore.Message{ID: messageID}, f.err
}

// waitDownloadMessage polls for the message id media.download reaches the seam
// with; media.download acks immediately and runs the download in a background
// goroutine, so the assertion cannot read the field synchronously.
func (f *fakeCommandActions) waitDownloadMessage(t *testing.T) string {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		f.mu.Lock()
		v := f.downloadMessage
		f.mu.Unlock()
		if v != "" {
			return v
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for media.download to reach the actions seam")
			return ""
		case <-time.After(2 * time.Millisecond):
		}
	}
}
func (f *fakeCommandActions) FetchProfilePicture(_ context.Context, jid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchJID = jid
	return "/cache/avatar.jpg", f.err
}
func (f *fakeCommandActions) SetPrivacySetting(_ context.Context, category, audience string, readReceipts bool) (app.PrivacySettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privacyCategory, f.privacyAudience, f.privacyRead = category, audience, readReceipts
	return app.PrivacySettings{LastSeen: audience, ReadReceipts: readReceipts}, f.err
}
func (f *fakeCommandActions) UpdateAppPreferences(_ context.Context, apply func(*app.AppPreferences)) (app.AppPreferences, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prefs == (app.AppPreferences{}) {
		f.prefs = app.DefaultAppPreferences()
	}
	prefs := f.prefs
	apply(&prefs)
	f.setPrefs = prefs
	f.prefs = prefs
	return prefs, f.err
}
func (f *fakeCommandActions) SetProfileStatus(_ context.Context, statusText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profileStatus = statusText
	return f.err
}
func (f *fakeCommandActions) UpdateBlocklist(_ context.Context, jid string, block bool) ([]app.BlockedContact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockJID, f.blocked = jid, block
	return []app.BlockedContact{{JID: jid}}, f.err
}
func (f *fakeCommandActions) SetStickerFavorite(_ context.Context, cacheKey, messageID string, favorite bool) (appstore.Sticker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.favoriteKey, f.favoriteMessage, f.favorite = cacheKey, messageID, favorite
	return appstore.Sticker{CacheKey: cacheKey, IsFavorite: favorite}, f.err
}
func (f *fakeCommandActions) DownloadSticker(_ context.Context, cacheKey string) (appstore.Sticker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadSticker = cacheKey
	return appstore.Sticker{CacheKey: cacheKey, LocalPath: "/cache/" + cacheKey + ".webp"}, f.err
}
func (f *fakeCommandActions) SetStickerPackInstalled(_ context.Context, packID string, installed bool) (appstore.StickerPack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installPackID, f.installed = packID, installed
	return appstore.StickerPack{ID: packID, Installed: installed}, f.err
}
func (f *fakeCommandActions) SearchChats(_ context.Context, query string, limit int) ([]appstore.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchChatsQuery, f.searchChatsLimit = query, limit
	return []appstore.Chat{{ID: "chat@s.whatsapp.net", Name: "Alice", LastMessage: "hi", LastMessageTime: 10}}, f.err
}
func (f *fakeCommandActions) SearchMessages(_ context.Context, query, chatID string, limit int, beforeMessageID string) ([]appstore.MessageSearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchMsgQuery, f.searchMsgChat, f.searchMsgLimit, f.searchMsgBefore = query, chatID, limit, beforeMessageID
	return []appstore.MessageSearchResult{
		{Message: appstore.Message{ID: "m2", ChatID: "chat@s.whatsapp.net", Text: "hello again", TimestampUnix: 20, SortSeq: 2, Direction: appstore.DirectionIncoming, Status: appstore.StatusDelivered}, ChatName: "Alice"},
		{Message: appstore.Message{ID: "m1", ChatID: "chat@s.whatsapp.net", Text: "hello", TimestampUnix: 10, SortSeq: 1, Direction: appstore.DirectionIncoming, Status: appstore.StatusDelivered}, ChatName: "Alice"},
	}, f.err
}
func (f *fakeCommandActions) CheckPhoneOnWhatsApp(_ context.Context, phone string) (app.PhoneCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkPhone = phone
	return app.PhoneCheck{Registered: true, JID: "123@s.whatsapp.net", DisplayName: "Alice", Phone: "+123"}, f.err
}

func startCommandTestServer(t *testing.T, actions *fakeCommandActions) (string, *Server) {
	t.Helper()
	socketPath, server := startTestServer(t)
	RegisterDaemonCommands(server, actions)
	return socketPath, server
}

func TestC1ChatCommands(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	cases := []struct {
		line string
		want func(t *testing.T)
	}{
		{`{"id":2,"method":"chat.mark_read","params":{"chat_id":"chat@s.whatsapp.net","up_to_message_id":"chat@s.whatsapp.net:m3"}}`, func(t *testing.T) {
			if actions.markReadChat != "chat@s.whatsapp.net" || actions.markReadUpTo != "chat@s.whatsapp.net:m3" {
				t.Fatalf("mark_read call = %q/%q", actions.markReadChat, actions.markReadUpTo)
			}
		}},
		{`{"id":3,"method":"chat.pin","params":{"chat_id":"chat@s.whatsapp.net","pinned":true}}`, func(t *testing.T) {
			if actions.pinnedChat != "chat@s.whatsapp.net" || !actions.pinned {
				t.Fatalf("pin call = %q/%v", actions.pinnedChat, actions.pinned)
			}
		}},
		{`{"id":4,"method":"chat.archive","params":{"chat_id":"chat@s.whatsapp.net","archived":true}}`, func(t *testing.T) {
			if actions.archivedChat != "chat@s.whatsapp.net" || !actions.archived {
				t.Fatalf("archive call = %q/%v", actions.archivedChat, actions.archived)
			}
		}},
		{`{"id":5,"method":"chat.mute","params":{"chat_id":"chat@s.whatsapp.net","muted":true,"duration_secs":60}}`, func(t *testing.T) {
			if actions.mutedChat != "chat@s.whatsapp.net" || !actions.muted || actions.muteDuration != time.Minute {
				t.Fatalf("mute call = %q/%v/%s", actions.mutedChat, actions.muted, actions.muteDuration)
			}
		}},
		{`{"id":6,"method":"chat.typing","params":{"chat_id":"chat@s.whatsapp.net","composing":true}}`, func(t *testing.T) {
			if actions.typingChat != "chat@s.whatsapp.net" || !actions.composing {
				t.Fatalf("typing call = %q/%v", actions.typingChat, actions.composing)
			}
		}},
	}

	for _, tc := range cases {
		c.sendLine(tc.line)
		msg := c.recv()
		if _, ok := msg["result"].(map[string]any); !ok {
			t.Fatalf("command failed: %v", msg)
		}
		tc.want(t)
	}

	c.sendLine(`{"id":7,"method":"chat.request_older","params":{"chat_id":"chat@s.whatsapp.net"}}`)
	msg := c.recv()
	result := msg["result"].(map[string]any)
	if result["requested"] != true || !actions.requested {
		t.Fatalf("request_older result/action = %v/%v", result, actions.requested)
	}

	c.sendLine(`{"id":8,"method":"chat.ensure_direct","params":{"jid":"123@s.whatsapp.net"}}`)
	msg = c.recv()
	result = msg["result"].(map[string]any)
	if result["chat_id"] != "chat-123@s.whatsapp.net" || actions.ensuredJID != "123@s.whatsapp.net" {
		t.Fatalf("ensure_direct result/action = %v/%q", result, actions.ensuredJID)
	}
}

func TestC2SendCommands(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"send.text","params":{"chat_id":"chat@s.whatsapp.net","text":"  hi  ","reply_to":"r1","mentions":[" a@s.whatsapp.net ",""]}}`)
	result := c.recv()["result"].(map[string]any)
	// Whitespace in user-authored text is preserved (only ids/mentions are trimmed).
	if result["message_id"] != "text-id" || actions.sendTextChat != "chat@s.whatsapp.net" || actions.sendTextText != "  hi  " || actions.sendTextReply != "r1" || len(actions.sendTextMentions) != 1 || actions.sendTextMentions[0] != "a@s.whatsapp.net" {
		t.Fatalf("send.text result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":3,"method":"send.media","params":{"chat_id":"chat@s.whatsapp.net","path":"/tmp/p.png","caption":" cap ","reply_to":"r2","mentions":["b@s.whatsapp.net"]}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "media-id" || actions.sendMediaPath != "/tmp/p.png" || actions.sendMediaCaption != " cap " || actions.sendMediaReply != "r2" || len(actions.sendMediaMentions) != 1 {
		t.Fatalf("send.media result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":4,"method":"send.sticker","params":{"chat_id":"chat@s.whatsapp.net","cache_key":"ck","reply_to":"r3"}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "sticker-id" || actions.sendStickerKey != "ck" || actions.sendStickerReply != "r3" {
		t.Fatalf("send.sticker result/action = %v/%+v", result, actions)
	}
}

func TestC2MessageAndMediaCommands(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	cases := []struct {
		line string
		want func(t *testing.T)
	}{
		{`{"id":2,"method":"message.react","params":{"message_id":"m1","emoji":""}}`, func(t *testing.T) {
			if actions.reactMessage != "m1" || actions.reactEmoji != "" {
				t.Fatalf("react call = %q/%q", actions.reactMessage, actions.reactEmoji)
			}
		}},
		{`{"id":3,"method":"message.edit","params":{"message_id":"m1","text":" new "}}`, func(t *testing.T) {
			if actions.editMessage != "m1" || actions.editText != " new " {
				t.Fatalf("edit call = %q/%q", actions.editMessage, actions.editText)
			}
		}},
		{`{"id":4,"method":"message.revoke","params":{"message_id":"m1"}}`, func(t *testing.T) {
			if actions.revokeMessage != "m1" {
				t.Fatalf("revoke call = %q", actions.revokeMessage)
			}
		}},
		{`{"id":5,"method":"message.delete","params":{"message_id":"m1"}}`, func(t *testing.T) {
			if actions.deleteMessage != "m1" {
				t.Fatalf("delete call = %q", actions.deleteMessage)
			}
		}},
		{`{"id":6,"method":"message.star","params":{"message_id":"m1","starred":true}}`, func(t *testing.T) {
			if actions.starMessage != "m1" || !actions.starred {
				t.Fatalf("star call = %q/%v", actions.starMessage, actions.starred)
			}
		}},
		{`{"id":7,"method":"message.pin","params":{"message_id":"m1","pinned":true,"duration_secs":60}}`, func(t *testing.T) {
			if actions.pinMessage != "m1" || !actions.messagePinned || actions.pinDuration != 60 {
				t.Fatalf("pin call = %q/%v/%d", actions.pinMessage, actions.messagePinned, actions.pinDuration)
			}
		}},
		{`{"id":8,"method":"media.download","params":{"message_id":"m1"}}`, func(t *testing.T) {
			if got := actions.waitDownloadMessage(t); got != "m1" {
				t.Fatalf("download call = %q", got)
			}
		}},
	}
	for _, tc := range cases {
		c.sendLine(tc.line)
		if _, ok := c.recv()["result"].(map[string]any); !ok {
			t.Fatalf("command failed for %s", tc.line)
		}
		tc.want(t)
	}

	c.sendLine(`{"id":9,"method":"message.forward","params":{"message_id":"m1","chat_ids":["a@s.whatsapp.net","a@s.whatsapp.net","b@s.whatsapp.net"]}}`)
	result := c.recv()["result"].(map[string]any)
	ids := result["message_ids"].([]any)
	if len(ids) != 2 || actions.forwardMessage != "m1" || len(actions.forwardChats) != 2 || actions.forwardChats[1] != "b@s.whatsapp.net" {
		t.Fatalf("forward result/action = %v/%+v", result, actions.forwardChats)
	}

	c.sendLine(`{"id":10,"method":"media.fetch_profile_picture","params":{"jid":" user@s.whatsapp.net "}}`)
	result = c.recv()["result"].(map[string]any)
	if result["path"] != "/cache/avatar.jpg" || actions.fetchJID != "user@s.whatsapp.net" {
		t.Fatalf("fetch profile result/action = %v/%q", result, actions.fetchJID)
	}
}

func TestC3SettingsContactAndStickerCommands(t *testing.T) {
	actions := &fakeCommandActions{prefs: app.AppPreferences{NotificationsEnabled: true, NotificationPreview: true}}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	cases := []struct {
		line string
		want func(t *testing.T)
	}{
		{`{"id":2,"method":"privacy.set","params":{"category":"last_seen","value":"contacts"}}`, func(t *testing.T) {
			if actions.privacyCategory != "last_seen" || actions.privacyAudience != "contacts" {
				t.Fatalf("privacy.set call = %q/%q", actions.privacyCategory, actions.privacyAudience)
			}
		}},
		{`{"id":3,"method":"privacy.set","params":{"category":"read_receipts","value":false}}`, func(t *testing.T) {
			if actions.privacyCategory != "read_receipts" || actions.privacyRead {
				t.Fatalf("read_receipts call = %q/%v", actions.privacyCategory, actions.privacyRead)
			}
		}},
		{`{"id":4,"method":"preferences.set","params":{"notification_preview":false,"auto_download_photos":true}}`, func(t *testing.T) {
			if !actions.setPrefs.NotificationsEnabled || actions.setPrefs.NotificationPreview || !actions.setPrefs.AutoDownloadPhotos {
				t.Fatalf("preferences patch = %+v", actions.setPrefs)
			}
		}},
		{`{"id":5,"method":"self.set_about","params":{"text":" out "}}`, func(t *testing.T) {
			if actions.profileStatus != " out " {
				t.Fatalf("self.set_about text = %q", actions.profileStatus)
			}
		}},
		{`{"id":6,"method":"contact.block","params":{"jid":" user@s.whatsapp.net ","blocked":true}}`, func(t *testing.T) {
			if actions.blockJID != "user@s.whatsapp.net" || !actions.blocked {
				t.Fatalf("contact.block call = %q/%v", actions.blockJID, actions.blocked)
			}
		}},
		{`{"id":7,"method":"sticker.favorite","params":{"message_id":"m1","favorite":true}}`, func(t *testing.T) {
			if actions.favoriteMessage != "m1" || !actions.favorite {
				t.Fatalf("sticker.favorite call = %q/%v", actions.favoriteMessage, actions.favorite)
			}
		}},
		{`{"id":8,"method":"sticker.download","params":{"cache_key":"ck"}}`, func(t *testing.T) {
			if actions.downloadSticker != "ck" {
				t.Fatalf("sticker.download call = %q", actions.downloadSticker)
			}
		}},
		{`{"id":9,"method":"sticker_pack.install","params":{"pack_id":"p1","installed":true}}`, func(t *testing.T) {
			if actions.installPackID != "p1" || !actions.installed {
				t.Fatalf("sticker_pack.install call = %q/%v", actions.installPackID, actions.installed)
			}
		}},
	}

	for _, tc := range cases {
		c.sendLine(tc.line)
		if _, ok := c.recv()["result"].(map[string]any); !ok {
			t.Fatalf("command failed: %s", tc.line)
		}
		tc.want(t)
	}
}

func TestC3Queries(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"search.chats","params":{"query":" ali ","limit":2}}`)
	result := c.recv()["result"].(map[string]any)
	chats := result["chats"].([]any)
	if len(chats) != 1 || chats[0].(map[string]any)["id"] != "chat@s.whatsapp.net" || actions.searchChatsQuery != "ali" || actions.searchChatsLimit != 2 {
		t.Fatalf("search.chats result/action = %v/%q/%d", result, actions.searchChatsQuery, actions.searchChatsLimit)
	}

	c.sendLine(`{"id":3,"method":"search.messages","params":{"query":" hello ","chat_id":"chat@s.whatsapp.net","limit":1,"before_message_id":"m3"}}`)
	result = c.recv()["result"].(map[string]any)
	messages := result["messages"].([]any)
	if len(messages) != 1 || result["has_more"] != true || actions.searchMsgQuery != "hello" || actions.searchMsgLimit != 2 || actions.searchMsgBefore != "m3" {
		t.Fatalf("search.messages result/action = %v/%q/%d/%q", result, actions.searchMsgQuery, actions.searchMsgLimit, actions.searchMsgBefore)
	}
	msg := messages[0].(map[string]any)
	if msg["id"] != "m2" || msg["chat_name"] != "Alice" || msg["fallback"] != "hello again" {
		t.Fatalf("search message row = %v", msg)
	}

	c.sendLine(`{"id":4,"method":"contacts.check_phone","params":{"phone":" +1 23 "}}`)
	result = c.recv()["result"].(map[string]any)
	if result["registered"] != true || result["jid"] != "123@s.whatsapp.net" || actions.checkPhone != "+1 23" {
		t.Fatalf("contacts.check_phone result/action = %v/%q", result, actions.checkPhone)
	}
}

func TestOpenChatRoutesToFocusedProtocolSession(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, server := startCommandTestServer(t, actions)
	unfocused := dialTest(t, socketPath)
	unfocused.hello()
	focused := dialTest(t, socketPath)
	focused.hello()

	if server.OpenChat("chat@s.whatsapp.net") {
		t.Fatal("open_chat delivered before any protocol frontend session existed")
	}

	unfocused.sendLine(`{"id":2,"method":"session.update","params":{"focused":false,"active_chat_id":"old@s.whatsapp.net"}}`)
	if _, ok := unfocused.recv()["result"].(map[string]any); !ok {
		t.Fatal("unfocused session.update failed")
	}
	focused.sendLine(`{"id":2,"method":"session.update","params":{"focused":true,"active_chat_id":"chat@s.whatsapp.net"}}`)
	if _, ok := focused.recv()["result"].(map[string]any); !ok {
		t.Fatal("focused session.update failed")
	}

	if !server.OpenChat(" chat@s.whatsapp.net ") {
		t.Fatal("open_chat was not delivered to a protocol frontend")
	}
	evt := focused.recvEvent()
	if evt["event"] != "open_chat" || evt["chat_id"] != "chat@s.whatsapp.net" {
		t.Fatalf("open_chat event = %v", evt)
	}
	if _, hasSub := evt["sub"]; hasSub {
		t.Fatalf("open_chat must be connection-directed, got sub in %v", evt)
	}
}

func TestC1SessionUpdateStartsAndEndsFrontendSession(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"session.update","params":{"focused":true,"active_chat_id":"chat@s.whatsapp.net"}}`)
	msg := c.recv()
	if _, ok := msg["result"].(map[string]any); !ok {
		t.Fatalf("session.update failed: %v", msg)
	}

	actions.mu.Lock()
	if len(actions.started) != 1 || len(actions.state) != 1 || !actions.state[0].focused || actions.state[0].activeChatID != "chat@s.whatsapp.net" {
		t.Fatalf("session calls before close: started=%v state=%v", actions.started, actions.state)
	}
	sessionID := actions.started[0]
	actions.mu.Unlock()

	_ = c.conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		actions.mu.Lock()
		ended := append([]string(nil), actions.ended...)
		actions.mu.Unlock()
		if len(ended) == 1 && ended[0] == sessionID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	actions.mu.Lock()
	defer actions.mu.Unlock()
	t.Fatalf("session not ended: started=%v ended=%v", actions.started, actions.ended)
}

func TestCommandValidationAndErrors(t *testing.T) {
	actions := &fakeCommandActions{}
	socketPath, _ := startCommandTestServer(t, actions)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"chat.mark_read","params":{"chat_id":"chat@s.whatsapp.net"}}`)
	if got := errorCode(t, c.recv()); got != CodeInvalidParams {
		t.Fatalf("missing up_to error = %s", got)
	}

	actions.err = sql.ErrNoRows
	c.sendLine(`{"id":3,"method":"chat.pin","params":{"chat_id":"missing@s.whatsapp.net","pinned":true}}`)
	if got := errorCode(t, c.recv()); got != CodeNotFound {
		t.Fatalf("sql no rows error = %s", got)
	}

	actions.err = app.NewCommandError(app.CommandErrorNotConnected, "offline")
	c.sendLine(`{"id":4,"method":"daemon.reconnect"}`)
	if got := errorCode(t, c.recv()); got != CodeNotConnected {
		t.Fatalf("unavailable error = %s", got)
	}

	actions.err = nil
	c.sendLine(`{"id":5,"method":"send.media","params":{"chat_id":"chat@s.whatsapp.net"}}`)
	if got := errorCode(t, c.recv()); got != CodeInvalidParams {
		t.Fatalf("missing path error = %s", got)
	}

	c.sendLine(`{"id":6,"method":"message.pin","params":{"message_id":"m1"}}`)
	if got := errorCode(t, c.recv()); got != CodeInvalidParams {
		t.Fatalf("missing pinned error = %s", got)
	}

	actions.err = app.NewCommandError(app.CommandErrorExpired, "the edit window for this message has expired")
	c.sendLine(`{"id":7,"method":"message.edit","params":{"message_id":"m1","text":"new"}}`)
	if got := errorCode(t, c.recv()); got != CodeExpired {
		t.Fatalf("expired error = %s", got)
	}

	actions.err = app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	c.sendLine(`{"id":8,"method":"send.text","params":{"chat_id":"chat@s.whatsapp.net","text":"hi"}}`)
	if got := errorCode(t, c.recv()); got != CodeNotLoggedIn {
		t.Fatalf("not logged in error = %s", got)
	}
}
