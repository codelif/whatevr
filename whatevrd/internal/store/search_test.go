package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func searchTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, ctx
}

func saveSearchMessage(t *testing.T, db *DB, ctx context.Context, id, chatID, chatName, text string, ts int64) {
	t.Helper()
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        id,
		ChatID:    chatID,
		ChatName:  chatName,
		SenderID:  "sender-1",
		Text:      text,
		Timestamp: time.Unix(ts, 0),
		Direction: DirectionIncoming,
		Status:    StatusDelivered,
	}); err != nil {
		t.Fatalf("save message %s: %v", id, err)
	}
}

func ids(results []MessageSearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

func TestSearchMessages(t *testing.T) {
	db, ctx := searchTestDB(t)

	saveSearchMessage(t, db, ctx, "c1:m1", "c1", "Alice", "hello world", 100)
	saveSearchMessage(t, db, ctx, "c1:m2", "c1", "Alice", "the wonderful weather", 200)
	saveSearchMessage(t, db, ctx, "c2:m1", "c2", "Bob", "hello again from another chat", 300)

	// Plain term, all chats, newest first.
	got, err := db.SearchMessages(ctx, "hello", "", 10, "")
	if err != nil {
		t.Fatalf("search hello: %v", err)
	}
	if want := []string{"c2:m1", "c1:m1"}; !equalStrings(ids(got), want) {
		t.Fatalf("hello search = %v, want %v", ids(got), want)
	}
	if got[0].ChatName != "Bob" {
		t.Fatalf("expected chat name Bob, got %q", got[0].ChatName)
	}

	// Prefix match on the final token: "wo" hits "world" and "wonderful".
	got, err = db.SearchMessages(ctx, "wo", "", 10, "")
	if err != nil {
		t.Fatalf("search prefix: %v", err)
	}
	if want := []string{"c1:m2", "c1:m1"}; !equalStrings(ids(got), want) {
		t.Fatalf("prefix search = %v, want %v", ids(got), want)
	}

	// Scope to a single chat.
	got, err = db.SearchMessages(ctx, "hello", "c1", 10, "")
	if err != nil {
		t.Fatalf("search scoped: %v", err)
	}
	if want := []string{"c1:m1"}; !equalStrings(ids(got), want) {
		t.Fatalf("scoped search = %v, want %v", ids(got), want)
	}

	// Blank / no-token queries return nothing without error.
	for _, q := range []string{"", "   "} {
		if got, err := db.SearchMessages(ctx, q, "", 10, ""); err != nil || len(got) != 0 {
			t.Fatalf("blank query %q = (%v, %v), want (nil, nil)", q, got, err)
		}
	}
}

func TestSearchMessagesReflectsEdits(t *testing.T) {
	db, ctx := searchTestDB(t)
	saveSearchMessage(t, db, ctx, "c1:m1", "c1", "Alice", "original banana text", 100)

	if got, err := db.SearchMessages(ctx, "banana", "", 10, ""); err != nil || len(got) != 1 {
		t.Fatalf("pre-edit banana search = (%v, %v), want one hit", got, err)
	}

	if _, _, _, err := db.UpdateMessageText(ctx, "c1:m1", "edited cherry text", nil); err != nil {
		t.Fatalf("edit: %v", err)
	}

	// Old term gone, new term indexed.
	if got, err := db.SearchMessages(ctx, "banana", "", 10, ""); err != nil || len(got) != 0 {
		t.Fatalf("post-edit banana search = (%v, %v), want no hits", got, err)
	}
	if got, err := db.SearchMessages(ctx, "cherry", "", 10, ""); err != nil || len(got) != 1 {
		t.Fatalf("post-edit cherry search = (%v, %v), want one hit", got, err)
	}
}

func TestSearchChats(t *testing.T) {
	db, ctx := searchTestDB(t)
	saveSearchMessage(t, db, ctx, "c1:m1", "c1", "Alice Cooper", "hi", 100)
	saveSearchMessage(t, db, ctx, "c2:m1", "c2", "Bob Dylan", "hi", 200)

	got, err := db.SearchChats(ctx, "ali", 10)
	if err != nil {
		t.Fatalf("search chats: %v", err)
	}
	if len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("chat search = %v, want [c1]", got)
	}

	// Case-insensitive.
	if got, err := db.SearchChats(ctx, "DYLAN", 10); err != nil || len(got) != 1 || got[0].ID != "c2" {
		t.Fatalf("case-insensitive chat search = (%v, %v), want [c2]", got, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
