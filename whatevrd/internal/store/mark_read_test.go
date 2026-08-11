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

	// Empty input still recomputes; with a genuinely unread row left the badge
	// holds at 1 and nothing is reported as changed.
	chat, changed, err = db.MarkMessagesReadByIDs(ctx, "chat-1", nil)
	if err != nil {
		t.Fatalf("MarkMessagesReadByIDs (empty): %v", err)
	}
	if changed || chat.UnreadCount != 1 {
		t.Fatalf("empty mark-read changed=%v unread=%d, want false/1", changed, chat.UnreadCount)
	}
}

// A pre-v7 database can hold a badge with no unread message rows behind it,
// which mark-read could never clear. Reopening repairs it.
func TestSchemaV7RepairsUnreadStateWithoutUnreadRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "whatevrd.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	seedUnreadChat(t, ctx, db, "chat-1", 100, 200, 300)

	// Rewind to what the old history-sync path left behind: a badge from the
	// phone with every incoming row flagged read.
	if _, err := db.conn.ExecContext(ctx, `UPDATE messages SET is_read = 1 WHERE chat_id = ?`, "chat-1"); err != nil {
		t.Fatalf("mark rows read: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `UPDATE chats SET unread_count = 2 WHERE id = ?`, "chat-1"); err != nil {
		t.Fatalf("poison badge: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `PRAGMA user_version = 6`); err != nil {
		t.Fatalf("rewind user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()

	chat, err := db.GetChat(ctx, "chat-1")
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 2 {
		t.Fatalf("repair changed the badge: unread=%d, want 2", chat.UnreadCount)
	}

	// The two newest rows now back the badge, so mark-read can act on them.
	candidates, err := db.ReadCandidatesForChat(ctx, "chat-1")
	if err != nil {
		t.Fatalf("read candidates: %v", err)
	}
	if len(candidates) != 2 || candidates[0].InternalID != "chat-1:msg-2" || candidates[1].InternalID != "chat-1:msg-3" {
		t.Fatalf("unexpected repaired candidates: %+v", candidates)
	}

	chat, _, err = db.MarkChatReadUpTo(ctx, "chat-1", 300)
	if err != nil {
		t.Fatalf("MarkChatReadUpTo: %v", err)
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("repaired badge did not clear: unread=%d", chat.UnreadCount)
	}
}
