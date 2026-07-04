package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func chatsDeleteTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, ctx
}

func seedChatWithMessages(t *testing.T, db *DB, ctx context.Context, chatID string) {
	t.Helper()
	for i, id := range []string{"msg-1", "msg-2"} {
		if _, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:          chatID + ":" + id,
			ChatID:      chatID,
			SenderID:    "peer@s.whatsapp.net",
			Text:        "hello " + id,
			Timestamp:   time.Unix(int64(100+i), 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		}); err != nil {
			t.Fatalf("save message %s: %v", id, err)
		}
	}
	if err := db.UpsertMessageReceipt(ctx, chatID+":msg-1", chatID, "peer@s.whatsapp.net", ReceiptKindDelivered, time.Unix(200, 0)); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
}

func TestDeleteChatRemovesChatMessagesAndReceipts(t *testing.T) {
	db, ctx := chatsDeleteTestDB(t)
	const chatID = "peer@s.whatsapp.net"
	seedChatWithMessages(t, db, ctx, chatID)

	existed, err := db.DeleteChat(ctx, chatID)
	if err != nil {
		t.Fatalf("DeleteChat: %v", err)
	}
	if !existed {
		t.Fatal("DeleteChat reported missing chat")
	}

	if _, err := db.GetChat(ctx, chatID); err == nil {
		t.Fatal("chat row survived deletion")
	}
	var messages, receipts int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE chat_id = ?`, chatID).Scan(&messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_receipts WHERE chat_id = ?`, chatID).Scan(&receipts); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if messages != 0 || receipts != 0 {
		t.Fatalf("expected no rows after delete, got %d messages, %d receipts", messages, receipts)
	}

	existed, err = db.DeleteChat(ctx, chatID)
	if err != nil || existed {
		t.Fatalf("second DeleteChat = %v, %v; want false, nil", existed, err)
	}
}

func TestClearChatMessagesKeepsChatRow(t *testing.T) {
	db, ctx := chatsDeleteTestDB(t)
	const chatID = "peer@s.whatsapp.net"
	seedChatWithMessages(t, db, ctx, chatID)

	chat, existed, err := db.ClearChatMessages(ctx, chatID)
	if err != nil {
		t.Fatalf("ClearChatMessages: %v", err)
	}
	if !existed {
		t.Fatal("ClearChatMessages reported missing chat")
	}
	if chat.ID != chatID {
		t.Fatalf("returned chat %q, want %q", chat.ID, chatID)
	}
	if chat.LastMessage != "" || chat.UnreadCount != 0 {
		t.Fatalf("cleared chat kept summary %q / unread %d", chat.LastMessage, chat.UnreadCount)
	}

	var messages int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE chat_id = ?`, chatID).Scan(&messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messages != 0 {
		t.Fatalf("expected no messages after clear, got %d", messages)
	}
	if _, err := db.GetChat(ctx, chatID); err != nil {
		t.Fatalf("chat row missing after clear: %v", err)
	}
}
