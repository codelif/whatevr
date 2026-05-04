package wa

import "testing"

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
