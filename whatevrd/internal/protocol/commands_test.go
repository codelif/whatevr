package protocol

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

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
func (f *fakeCommandActions) FetchProfilePicture(_ context.Context, jid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchJID = jid
	return "/cache/avatar.jpg", f.err
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
	if result["message_id"] != "text-id" || actions.sendTextChat != "chat@s.whatsapp.net" || actions.sendTextText != "hi" || actions.sendTextReply != "r1" || len(actions.sendTextMentions) != 1 || actions.sendTextMentions[0] != "a@s.whatsapp.net" {
		t.Fatalf("send.text result/action = %v/%+v", result, actions)
	}

	c.sendLine(`{"id":3,"method":"send.media","params":{"chat_id":"chat@s.whatsapp.net","path":"/tmp/p.png","caption":" cap ","reply_to":"r2","mentions":["b@s.whatsapp.net"]}}`)
	result = c.recv()["result"].(map[string]any)
	if result["message_id"] != "media-id" || actions.sendMediaPath != "/tmp/p.png" || actions.sendMediaCaption != "cap" || actions.sendMediaReply != "r2" || len(actions.sendMediaMentions) != 1 {
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
			if actions.editMessage != "m1" || actions.editText != "new" {
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
			if actions.downloadMessage != "m1" {
				t.Fatalf("download call = %q", actions.downloadMessage)
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

	actions.err = grpcstatus.Error(codes.Unavailable, "offline")
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

	actions.err = grpcstatus.Error(codes.FailedPrecondition, "the edit window for this message has expired")
	c.sendLine(`{"id":7,"method":"message.edit","params":{"message_id":"m1","text":"new"}}`)
	if got := errorCode(t, c.recv()); got != CodeExpired {
		t.Fatalf("expired error = %s", got)
	}

	actions.err = grpcstatus.Error(codes.FailedPrecondition, "WhatsApp session is not logged in")
	c.sendLine(`{"id":8,"method":"send.text","params":{"chat_id":"chat@s.whatsapp.net","text":"hi"}}`)
	if got := errorCode(t, c.recv()); got != CodeNotLoggedIn {
		t.Fatalf("not logged in error = %s", got)
	}
}
