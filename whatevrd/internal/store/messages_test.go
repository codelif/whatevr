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

func TestListChatsSortsByMostRecentMessage(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-a:1",
		ChatID:      "chat-a",
		ChatName:    "Alpha",
		SenderID:    "sender-a",
		Text:        "older",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusReceived,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save alpha: %v", err)
	}

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-b:1",
		ChatID:      "chat-b",
		ChatName:    "Beta",
		SenderID:    "sender-b",
		Text:        "newer",
		Timestamp:   time.Unix(200, 0),
		Direction:   DirectionIncoming,
		Status:      StatusReceived,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save beta: %v", err)
	}

	chats, err := db.ListChats(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chats))
	}
	if chats[0].ID != "chat-b" || chats[1].ID != "chat-a" {
		t.Fatalf("unexpected chat order: %+v", chats)
	}
}

func TestListMessagesReturnsAscendingOrderAndPagination(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, item := range []struct {
		id string
		ts int64
	}{
		{id: "chat-1:1", ts: 100},
		{id: "chat-1:2", ts: 200},
		{id: "chat-1:3", ts: 300},
	} {
		_, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:          item.id,
			ChatID:      "chat-1",
			ChatName:    "Test Chat",
			SenderID:    "sender-1",
			Text:        item.id,
			Timestamp:   time.Unix(item.ts, 0),
			Direction:   DirectionIncoming,
			Status:      StatusReceived,
			CountUnread: true,
		})
		if err != nil {
			t.Fatalf("save %s: %v", item.id, err)
		}
	}

	latestTwo, err := db.ListMessages(ctx, "chat-1", 2, "")
	if err != nil {
		t.Fatalf("list latest messages: %v", err)
	}
	if len(latestTwo) != 2 || latestTwo[0].ID != "chat-1:2" || latestTwo[1].ID != "chat-1:3" {
		t.Fatalf("unexpected latest messages: %+v", latestTwo)
	}

	older, err := db.ListMessages(ctx, "chat-1", 2, "chat-1:2")
	if err != nil {
		t.Fatalf("list paginated messages: %v", err)
	}
	if len(older) != 1 || older[0].ID != "chat-1:1" {
		t.Fatalf("unexpected paginated messages: %+v", older)
	}
}

func TestMarkChatReadClearsUnreadCount(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusReceived,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}

	chat, err := db.MarkChatRead(ctx, "chat-1")
	if err != nil {
		t.Fatalf("mark chat read: %v", err)
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("expected unread_count 0, got %+v", chat)
	}
}

func TestUpdateMessageStatusProgression(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:sent-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "me",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionOutgoing,
		Status:      StatusSent,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save outgoing message: %v", err)
	}

	message, changed, err := db.UpdateMessageStatus(ctx, "chat-1:sent-1", StatusDelivered)
	if err != nil {
		t.Fatalf("set delivered: %v", err)
	}
	if !changed || message.Status != StatusDelivered {
		t.Fatalf("unexpected delivered update: %+v changed=%v", message, changed)
	}

	message, changed, err = db.UpdateMessageStatus(ctx, "chat-1:sent-1", StatusRead)
	if err != nil {
		t.Fatalf("set read: %v", err)
	}
	if !changed || message.Status != StatusRead {
		t.Fatalf("unexpected read update: %+v changed=%v", message, changed)
	}

	message, changed, err = db.UpdateMessageStatus(ctx, "chat-1:sent-1", StatusDelivered)
	if err != nil {
		t.Fatalf("attempt downgrade: %v", err)
	}
	if changed || message.Status != StatusRead {
		t.Fatalf("unexpected downgrade result: %+v changed=%v", message, changed)
	}
}
