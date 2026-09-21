package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestRevokeKeepsContentWhenAsked locks in anti-delete: with keepContent the
// row keeps its text and stays previewable, flagged revoked.
func TestRevokeKeepsContentWhenAsked(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        "chat-1:msg-1",
		ChatID:    "chat-1",
		ChatName:  "Test Chat",
		SenderID:  "sender-1",
		Text:      "secret",
		Timestamp: time.Unix(100, 0),
		Direction: DirectionIncoming,
		Status:    StatusDelivered,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
	kept, _, changed, err := db.MarkMessageRevoked(ctx, "chat-1:msg-1", true)
	if err != nil || !changed {
		t.Fatalf("revoke keep = %+v, %v, %v; want changed", kept, changed, err)
	}
	if !kept.IsRevoked || kept.Text != "secret" {
		t.Fatalf("kept row = %+v; want revoked with text intact", kept)
	}
	chat, err := db.GetChat(ctx, "chat-1")
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.LastMessage != "secret" {
		t.Fatalf("chat preview = %q; want kept content", chat.LastMessage)
	}
}

// TestEditHistoryRecordsVersions locks in that every real text change files
// the superseded body, oldest first, and the live row holds the newest.
func TestEditHistoryRecordsVersions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        "chat-1:msg-1",
		ChatID:    "chat-1",
		ChatName:  "Test Chat",
		SenderID:  "me",
		Text:      "v1",
		Timestamp: time.Unix(100, 0),
		Direction: DirectionOutgoing,
		Status:    StatusSent,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
	if _, _, changed, err := db.UpdateMessageText(ctx, "chat-1:msg-1", "v2", nil); err != nil || !changed {
		t.Fatalf("edit to v2 = %v, %v; want changed", changed, err)
	}
	if _, _, changed, err := db.UpdateMessageText(ctx, "chat-1:msg-1", "v3", nil); err != nil || !changed {
		t.Fatalf("edit to v3 = %v, %v; want changed", changed, err)
	}
	// Re-applying the same text is a no-op and must not file history.
	if _, _, changed, err := db.UpdateMessageText(ctx, "chat-1:msg-1", "v3", nil); err != nil || changed {
		t.Fatalf("re-apply v3 = %v, %v; want no-op", changed, err)
	}
	edits, err := db.ListMessageEdits(ctx, "chat-1:msg-1")
	if err != nil {
		t.Fatalf("list edits: %v", err)
	}
	if len(edits) != 2 || edits[0].Text != "v1" || edits[1].Text != "v2" {
		t.Fatalf("edits = %+v; want [v1 v2]", edits)
	}
	msg, err := db.GetMessage(ctx, "chat-1:msg-1")
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if msg.Text != "v3" || !msg.IsEdited {
		t.Fatalf("live row = %+v; want v3 edited", msg)
	}
}

// TestStatusKeepSenders locks in the keep/archive flag lifecycle.
func TestStatusKeepSenders(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.SetStatusKeepSender(ctx, "a@s.whatsapp.net", true); err != nil {
		t.Fatalf("keep a: %v", err)
	}
	if err := db.SetStatusKeepSender(ctx, "b@s.whatsapp.net", true); err != nil {
		t.Fatalf("keep b: %v", err)
	}
	if err := db.SetStatusKeepSender(ctx, "a@s.whatsapp.net", false); err != nil {
		t.Fatalf("unkeep a: %v", err)
	}
	kept, err := db.ListKeptStatusSenders(ctx)
	if err != nil {
		t.Fatalf("list kept: %v", err)
	}
	if len(kept) != 1 || kept[0] != "b@s.whatsapp.net" {
		t.Fatalf("kept = %v; want [b@s.whatsapp.net]", kept)
	}
}

// TestMarkAllChatsRead locks in that one call clears every badge and every
// unread row, reporting the touched chats.
func TestMarkAllChatsRead(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	seed := func(chat, id string, unread bool) {
		if _, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:          id,
			ChatID:      chat,
			ChatName:    chat,
			SenderID:    "peer",
			Text:        "hi",
			Timestamp:   time.Unix(100, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: unread,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("chat-1", "chat-1:m1", true)
	seed("chat-1", "chat-1:m2", true)
	seed("chat-2", "chat-2:m1", true)
	seed("chat-3", "chat-3:m1", false)

	ids, err := db.MarkAllChatsRead(ctx)
	if err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("touched = %v; want 2 chats", ids)
	}
	for _, chatID := range []string{"chat-1", "chat-2", "chat-3"} {
		chat, err := db.GetChat(ctx, chatID)
		if err != nil {
			t.Fatalf("get %s: %v", chatID, err)
		}
		if chat.UnreadCount != 0 {
			t.Fatalf("%s unread = %d; want 0", chatID, chat.UnreadCount)
		}
	}
}
