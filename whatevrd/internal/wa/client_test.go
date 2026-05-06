package wa

import (
	"fmt"
	"testing"
	"time"

	"whatevrd/internal/app"
)

func TestShouldNotifyChatNoFocusedSession(t *testing.T) {
	c := &Client{frontendSessions: make(map[string]frontendSession)}
	c.FrontendSessionStarted("s1")
	c.FrontendSessionStateChanged("s1", false, "chat-a")

	if !c.ShouldNotifyChat("chat-a") {
		t.Fatal("unfocused session should not suppress notifications")
	}
}

func TestShouldNotifyChatFocusedSameChat(t *testing.T) {
	c := &Client{frontendSessions: make(map[string]frontendSession)}
	c.FrontendSessionStarted("s1")
	c.FrontendSessionStateChanged("s1", true, "chat-a")

	if c.ShouldNotifyChat("chat-a") {
		t.Fatal("focused active chat should suppress notifications")
	}
}

func TestShouldNotifyChatFocusedDifferentChat(t *testing.T) {
	c := &Client{frontendSessions: make(map[string]frontendSession)}
	c.FrontendSessionStarted("s1")
	c.FrontendSessionStateChanged("s1", true, "chat-a")

	if !c.ShouldNotifyChat("chat-b") {
		t.Fatal("focused different chat should not suppress notifications")
	}
}

func TestShouldNotifyChatSessionEndClearsSuppression(t *testing.T) {
	c := &Client{frontendSessions: make(map[string]frontendSession)}
	c.FrontendSessionStarted("s1")
	c.FrontendSessionStateChanged("s1", true, "chat-a")
	c.FrontendSessionEnded("s1")

	if !c.ShouldNotifyChat("chat-a") {
		t.Fatal("ended session should not suppress notifications")
	}
}

func TestShouldNotifyChatAnyFocusedSessionSuppresses(t *testing.T) {
	c := &Client{frontendSessions: make(map[string]frontendSession)}
	c.FrontendSessionStarted("s1")
	c.FrontendSessionStarted("s2")
	c.FrontendSessionStateChanged("s1", false, "chat-a")
	c.FrontendSessionStateChanged("s2", true, "chat-a")

	if c.ShouldNotifyChat("chat-a") {
		t.Fatal("any focused session on chat should suppress notifications")
	}
}

func TestConnectionRetryKeepsQRLoginOnLoginRetry(t *testing.T) {
	retry := connectionRetry(3, fmt.Errorf("wrapped: %w", errQRLoginRetry))

	if retry.attempt != 0 {
		t.Fatalf("attempt = %d, want 0", retry.attempt)
	}
	if retry.delay != qrRetryDelay {
		t.Fatalf("delay = %v, want %v", retry.delay, qrRetryDelay)
	}
	if retry.state != app.StateNeedLogin {
		t.Fatalf("state = %v, want %v", retry.state, app.StateNeedLogin)
	}
	if retry.detail != "" {
		t.Fatalf("detail = %q, want empty to preserve QR detail", retry.detail)
	}
	if retry.canReconnect {
		t.Fatal("canReconnect = true, want false")
	}
	if retry.nextRetryUnix {
		t.Fatal("nextRetryUnix = true, want false")
	}
}

func TestConnectionRetryMarksNonQRFailuresOffline(t *testing.T) {
	retry := connectionRetry(0, fmt.Errorf("connect failed"))

	if retry.attempt != 1 {
		t.Fatalf("attempt = %d, want 1", retry.attempt)
	}
	if retry.delay < connBackoffBase || retry.delay >= connBackoffBase+time.Second {
		t.Fatalf("delay = %v, want first backoff range", retry.delay)
	}
	if retry.state != app.StateOffline {
		t.Fatalf("state = %v, want %v", retry.state, app.StateOffline)
	}
	if retry.detail == "" {
		t.Fatal("detail is empty, want retry detail")
	}
	if !retry.canReconnect {
		t.Fatal("canReconnect = false, want true")
	}
	if !retry.nextRetryUnix {
		t.Fatal("nextRetryUnix = false, want true")
	}
}
