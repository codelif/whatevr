package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveTextMessageInsertsOnceAndUpdatesChat(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	input := TextMessageInput{
		ID:          "chat-1:msg-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusReceived,
		CountUnread: true,
	}

	first, err := db.SaveTextMessage(ctx, input)
	if err != nil {
		t.Fatalf("save first message: %v", err)
	}
	if !first.Inserted {
		t.Fatal("expected first save to insert")
	}
	if first.Chat.LastMessage != "hello" || first.Chat.LastMessageTime != 100 || first.Chat.UnreadCount != 1 {
		t.Fatalf("unexpected chat after first save: %+v", first.Chat)
	}

	second, err := db.SaveTextMessage(ctx, input)
	if err != nil {
		t.Fatalf("save duplicate message: %v", err)
	}
	if second.Inserted {
		t.Fatal("expected duplicate save to be ignored")
	}
	if second.Chat.UnreadCount != 1 {
		t.Fatalf("duplicate changed unread count: %+v", second.Chat)
	}
}

func TestSaveTextMessageDoesNotUnreadOutgoingMessages(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	saved, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        "chat-1:msg-2",
		ChatID:    "chat-1",
		ChatName:  "Test Chat",
		SenderID:  "me",
		Text:      "from another linked device",
		Timestamp: time.Unix(200, 0),
		Direction: DirectionOutgoing,
		Status:    StatusSent,
	})
	if err != nil {
		t.Fatalf("save outgoing message: %v", err)
	}
	if saved.Chat.UnreadCount != 0 {
		t.Fatalf("outgoing message changed unread count: %+v", saved.Chat)
	}
}

func TestSaveTextMessageDoesNotRegressChatSummary(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	latest, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:latest",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "latest",
		Timestamp:   time.Unix(200, 0),
		Direction:   DirectionIncoming,
		Status:      StatusReceived,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save latest message: %v", err)
	}
	if latest.Chat.LastMessage != "latest" {
		t.Fatalf("unexpected latest chat summary: %+v", latest.Chat)
	}

	older, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:older",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "older",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusReceived,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save older message: %v", err)
	}
	if older.Chat.LastMessage != "latest" || older.Chat.LastMessageTime != 200 {
		t.Fatalf("older message regressed chat summary: %+v", older.Chat)
	}
}
