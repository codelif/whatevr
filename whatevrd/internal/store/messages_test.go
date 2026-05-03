package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
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
		Status:      StatusDelivered,
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
		Status:      StatusDelivered,
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
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save older message: %v", err)
	}
	if older.Chat.LastMessage != "latest" || older.Chat.LastMessageTime != 200 {
		t.Fatalf("older message regressed chat summary: %+v", older.Chat)
	}
}

func TestSaveTextMessageUpdatesChatNameAfterNumericFallback(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "917060029183@s.whatsapp.net"
	first, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":msg-1",
		ChatID:      chatID,
		SenderID:    chatID,
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save fallback message: %v", err)
	}
	if first.Chat.Name != chatID {
		t.Fatalf("expected fallback chat name %q, got %+v", chatID, first.Chat)
	}

	second, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":msg-2",
		ChatID:      chatID,
		ChatName:    "Alice",
		SenderID:    chatID,
		Text:        "history",
		Timestamp:   time.Unix(90, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save named message: %v", err)
	}
	if second.Chat.Name != "Alice" {
		t.Fatalf("expected chat name to update, got %+v", second.Chat)
	}
	if second.Chat.LastMessage != "hello" || second.Chat.LastMessageTime != 100 {
		t.Fatalf("older named message regressed chat summary: %+v", second.Chat)
	}
}

func TestUpdateChatNameUpdatesExistingChat(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "917060029183@s.whatsapp.net"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":msg-1",
		ChatID:      chatID,
		SenderID:    chatID,
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}

	chat, changed, err := db.UpdateChatName(ctx, chatID, "Alice")
	if err != nil {
		t.Fatalf("update chat name: %v", err)
	}
	if !changed || chat.Name != "Alice" {
		t.Fatalf("expected changed chat name, got changed=%v chat=%+v", changed, chat)
	}

	_, changed, err = db.UpdateChatName(ctx, chatID, "Alice")
	if err != nil {
		t.Fatalf("update same chat name: %v", err)
	}
	if changed {
		t.Fatal("expected same chat name update to be ignored")
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
		Status:      StatusDelivered,
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
		Status:      StatusDelivered,
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
			Status:      StatusDelivered,
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
		Status:      StatusDelivered,
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

func TestClearSessionDataDeletesChatsMessagesAndAppState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `INSERT INTO app_state (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatalf("insert app state: %v", err)
	}

	if err := db.ClearSessionData(ctx); err != nil {
		t.Fatalf("clear session data: %v", err)
	}

	chats, err := db.ListChats(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 0 {
		t.Fatalf("expected no chats after clear, got %+v", chats)
	}
	if _, err := db.GetMessage(ctx, "chat-1:1"); err != sql.ErrNoRows {
		t.Fatalf("expected message to be deleted, got %v", err)
	}

	var stateCount int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_state`).Scan(&stateCount); err != nil {
		t.Fatalf("count app state: %v", err)
	}
	if stateCount != 0 {
		t.Fatalf("expected app_state to be empty, got %d rows", stateCount)
	}
}

func TestReadCandidatesAndMarkMessagesRead(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:incoming-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "123@s.whatsapp.net",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save incoming unread: %v", err)
	}

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:incoming-history",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "123@s.whatsapp.net",
		Text:        "history",
		Timestamp:   time.Unix(90, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save incoming history: %v", err)
	}

	candidates, err := db.ReadCandidatesForChat(ctx, "chat-1")
	if err != nil {
		t.Fatalf("read candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ExternalID != "incoming-1" {
		t.Fatalf("unexpected read candidates: %+v", candidates)
	}

	chat, err := db.MarkMessagesRead(ctx, "chat-1")
	if err != nil {
		t.Fatalf("mark messages read: %v", err)
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("expected unread count 0 after mark read, got %+v", chat)
	}

	message, err := db.GetMessage(ctx, "chat-1:incoming-1")
	if err != nil {
		t.Fatalf("get message after mark read: %v", err)
	}
	if !message.IsRead {
		t.Fatalf("expected message to be read after mark read: %+v", message)
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

func TestListLIDChats(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, input := range []TextMessageInput{
		{
			ID:          "30413133201647@lid:lid-1",
			ChatID:      "30413133201647@lid",
			ChatName:    "LID Chat",
			SenderID:    "30413133201647@lid",
			Text:        "lid",
			Timestamp:   time.Unix(100, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		},
		{
			ID:          "917060029183@s.whatsapp.net:pn-1",
			ChatID:      "917060029183@s.whatsapp.net",
			ChatName:    "PN Chat",
			SenderID:    "917060029183@s.whatsapp.net",
			Text:        "pn",
			Timestamp:   time.Unix(200, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		},
		{
			ID:          "12345-67890@g.us:group-1",
			ChatID:      "12345-67890@g.us",
			ChatName:    "Group",
			SenderID:    "917060029183@s.whatsapp.net",
			Text:        "group",
			Timestamp:   time.Unix(300, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
			IsGroup:     true,
		},
	} {
		if _, err := db.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save %s: %v", input.ID, err)
		}
	}

	lidChats, err := db.ListLIDChats(ctx)
	if err != nil {
		t.Fatalf("list lid chats: %v", err)
	}

	want := []string{"30413133201647@lid"}
	if !reflect.DeepEqual(lidChats, want) {
		t.Fatalf("unexpected lid chats: got %v want %v", lidChats, want)
	}
}

func TestMigrateChatIDReturnsFalseWhenSourceMissing(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chat, migrated, err := db.MigrateChatID(ctx, "30413133201647@lid", "917060029183@s.whatsapp.net")
	if err != nil {
		t.Fatalf("migrate missing chat: %v", err)
	}
	if migrated {
		t.Fatal("expected migration to be skipped")
	}
	if chat != (Chat{}) {
		t.Fatalf("expected zero chat on skipped migration, got %+v", chat)
	}
}

func TestMigrateChatIDMovesMessagesToTarget(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	fromChatID := "30413133201647@lid"
	toChatID := "917060029183@s.whatsapp.net"

	for _, input := range []TextMessageInput{
		{
			ID:          fromChatID + ":msg-1",
			ChatID:      fromChatID,
			ChatName:    "Alice",
			SenderID:    fromChatID,
			Text:        "hello",
			Timestamp:   time.Unix(100, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		},
		{
			ID:          fromChatID + ":msg-2",
			ChatID:      fromChatID,
			ChatName:    "Alice",
			SenderID:    fromChatID,
			Text:        "history",
			Timestamp:   time.Unix(90, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: false,
		},
	} {
		if _, err := db.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save %s: %v", input.ID, err)
		}
	}

	chat, migrated, err := db.MigrateChatID(ctx, fromChatID, toChatID)
	if err != nil {
		t.Fatalf("migrate chat: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to happen")
	}
	if chat.ID != toChatID || chat.Name != "Alice" || chat.LastMessage != "hello" || chat.LastMessageTime != 100 || chat.UnreadCount != 1 {
		t.Fatalf("unexpected migrated chat: %+v", chat)
	}

	if _, err := db.GetChat(ctx, fromChatID); err != sql.ErrNoRows {
		t.Fatalf("expected source chat to be deleted, got %v", err)
	}

	messages, err := db.ListMessages(ctx, toChatID, 10, "")
	if err != nil {
		t.Fatalf("list migrated messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 migrated messages, got %d", len(messages))
	}
	if messages[0].ID != toChatID+":msg-2" || messages[0].ChatID != toChatID {
		t.Fatalf("unexpected first migrated message: %+v", messages[0])
	}
	if messages[1].ID != toChatID+":msg-1" || messages[1].ChatID != toChatID {
		t.Fatalf("unexpected second migrated message: %+v", messages[1])
	}

	if _, err := db.GetMessage(ctx, fromChatID+":msg-1"); err != sql.ErrNoRows {
		t.Fatalf("expected source message to be deleted, got %v", err)
	}
	if _, err := db.GetMessage(ctx, toChatID+":msg-1"); err != nil {
		t.Fatalf("expected rewritten message to exist: %v", err)
	}
}

func TestMigrateChatIDMergesExistingTargetWithoutDoubleCountingUnread(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	fromChatID := "30413133201647@lid"
	toChatID := "917060029183@s.whatsapp.net"

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          fromChatID + ":dup-1",
		ChatID:      fromChatID,
		ChatName:    "Alice",
		SenderID:    fromChatID,
		Text:        "duplicate",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save lid message: %v", err)
	}

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          toChatID + ":dup-1",
		ChatID:      toChatID,
		ChatName:    "",
		SenderID:    toChatID,
		Text:        "newer target",
		Timestamp:   time.Unix(200, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save target message: %v", err)
	}

	chat, migrated, err := db.MigrateChatID(ctx, fromChatID, toChatID)
	if err != nil {
		t.Fatalf("migrate chat: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to happen")
	}
	if chat.Name != "Alice" {
		t.Fatalf("expected target name to be replaced from default id, got %+v", chat)
	}
	if chat.LastMessage != "newer target" || chat.LastMessageTime != 200 {
		t.Fatalf("expected newer target summary to win, got %+v", chat)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("expected unread count to be recomputed to 1, got %+v", chat)
	}

	messages, err := db.ListMessages(ctx, toChatID, 10, "")
	if err != nil {
		t.Fatalf("list merged messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected duplicate message to be deduped, got %d", len(messages))
	}
}
