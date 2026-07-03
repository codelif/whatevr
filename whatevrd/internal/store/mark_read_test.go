package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func seedUnreadChat(t *testing.T, ctx context.Context, db *DB, chatID string, timestamps ...int64) {
	t.Helper()
	for i, ts := range timestamps {
		if _, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:          fmt.Sprintf("%s:msg-%d", chatID, i+1),
			ChatID:      chatID,
			ChatName:    "Test Chat",
			SenderID:    "sender-1",
			Text:        fmt.Sprintf("hello %d", i+1),
			Timestamp:   time.Unix(ts, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		}); err != nil {
			t.Fatalf("seed message %d: %v", i+1, err)
		}
	}
}

func TestMarkChatReadUpToRespectsTimestampBound(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	seedUnreadChat(t, ctx, db, "chat-1", 100, 200, 300)

	// A mark-read horizon between the second and third message clears only
	// the first two.
	chat, changed, err := db.MarkChatReadUpTo(ctx, "chat-1", 250)
	if err != nil {
		t.Fatalf("MarkChatReadUpTo: %v", err)
	}
	if !changed {
		t.Fatal("expected partial mark-read to report a change")
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("unread after partial mark-read = %d, want 1", chat.UnreadCount)
	}
	first, err := db.GetMessage(ctx, "chat-1:msg-1")
	if err != nil {
		t.Fatalf("get first message: %v", err)
	}
	if !first.IsRead {
		t.Fatal("message below the horizon is still unread")
	}
	third, err := db.GetMessage(ctx, "chat-1:msg-3")
	if err != nil {
		t.Fatalf("get third message: %v", err)
	}
	if third.IsRead {
		t.Fatal("message above the horizon was marked read")
	}

	// A later horizon clears the rest.
	chat, changed, err = db.MarkChatReadUpTo(ctx, "chat-1", 400)
	if err != nil {
		t.Fatalf("MarkChatReadUpTo (full): %v", err)
	}
	if !changed || chat.UnreadCount != 0 {
		t.Fatalf("after full mark-read changed=%v unread=%d, want true/0", changed, chat.UnreadCount)
	}

	// Re-running is a no-op.
	_, changed, err = db.MarkChatReadUpTo(ctx, "chat-1", 400)
	if err != nil {
		t.Fatalf("MarkChatReadUpTo (repeat): %v", err)
	}
	if changed {
		t.Fatal("repeated mark-read reported a change")
	}
}

func TestMarkChatReadUpToUnknownChat(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, _, err := db.MarkChatReadUpTo(ctx, "missing-chat", 100); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("MarkChatReadUpTo on unknown chat = %v, want sql.ErrNoRows", err)
	}
	if _, err := db.GetChat(ctx, "missing-chat"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("mark-read created a chat row for an unknown chat")
	}
}

func TestMarkMessagesReadByIDsRecomputesBadge(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	seedUnreadChat(t, ctx, db, "chat-1", 100, 200, 300)

	chat, changed, err := db.MarkMessagesReadByIDs(ctx, "chat-1", []string{"chat-1:msg-1", "chat-1:msg-3"})
	if err != nil {
		t.Fatalf("MarkMessagesReadByIDs: %v", err)
	}
	if !changed || chat.UnreadCount != 1 {
		t.Fatalf("after subset mark-read changed=%v unread=%d, want true/1", changed, chat.UnreadCount)
	}

	// Unknown ids are ignored; already-read ids are no-ops.
	_, changed, err = db.MarkMessagesReadByIDs(ctx, "chat-1", []string{"chat-1:msg-1", "chat-1:nope"})
	if err != nil {
		t.Fatalf("MarkMessagesReadByIDs (repeat): %v", err)
	}
	if changed {
		t.Fatal("repeat/unknown-id mark-read reported a change")
	}

	// Empty input is a pure read of the chat.
	chat, changed, err = db.MarkMessagesReadByIDs(ctx, "chat-1", nil)
	if err != nil {
		t.Fatalf("MarkMessagesReadByIDs (empty): %v", err)
	}
	if changed || chat.UnreadCount != 1 {
		t.Fatalf("empty mark-read changed=%v unread=%d, want false/1", changed, chat.UnreadCount)
	}
}
