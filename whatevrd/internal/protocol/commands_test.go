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

func TestC1CommandValidationAndErrors(t *testing.T) {
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
}
