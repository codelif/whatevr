package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPinnedChatCountExcluding(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, chatID := range []string{"chat-1", "chat-2", "chat-3"} {
		if _, err := db.EnsureChat(ctx, chatID, chatID, false); err != nil {
			t.Fatalf("ensure chat %s: %v", chatID, err)
		}
	}
	if _, _, err := db.UpdateChatPinState(ctx, "chat-1", true, 100); err != nil {
		t.Fatalf("pin chat-1: %v", err)
	}
	if _, _, err := db.UpdateChatPinState(ctx, "chat-2", true, 200); err != nil {
		t.Fatalf("pin chat-2: %v", err)
	}

	count, err := db.PinnedChatCountExcluding(ctx, "chat-1")
	if err != nil {
		t.Fatalf("count excluding chat-1: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 pinned chat excluding chat-1, got %d", count)
	}

	count, err = db.PinnedChatCountExcluding(ctx, "chat-3")
	if err != nil {
		t.Fatalf("count excluding chat-3: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 pinned chats excluding unpinned chat-3, got %d", count)
	}
}

func TestReconcileChatPinsUpdatesStaleAndChangedPins(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, chatID := range []string{"stale", "same", "changed", "new"} {
		if _, err := db.EnsureChat(ctx, chatID, chatID, false); err != nil {
			t.Fatalf("ensure chat %s: %v", chatID, err)
		}
	}
	if _, _, err := db.UpdateChatPinState(ctx, "stale", true, 100); err != nil {
		t.Fatalf("pin stale: %v", err)
	}
	if _, _, err := db.UpdateChatPinState(ctx, "same", true, 200); err != nil {
		t.Fatalf("pin same: %v", err)
	}
	if _, _, err := db.UpdateChatPinState(ctx, "changed", true, 300); err != nil {
		t.Fatalf("pin changed: %v", err)
	}

	changed, err := db.ReconcileChatPins(ctx, map[string]uint32{
		"same":    200,
		"changed": 350,
		"new":     400,
	})
	if err != nil {
		t.Fatalf("reconcile pins: %v", err)
	}

	changedByID := make(map[string]Chat, len(changed))
	for _, chat := range changed {
		changedByID[chat.ID] = chat
	}
	for _, chatID := range []string{"stale", "changed", "new"} {
		if _, ok := changedByID[chatID]; !ok {
			t.Fatalf("expected changed chat %s in %+v", chatID, changedByID)
		}
	}
	if _, ok := changedByID["same"]; ok {
		t.Fatalf("unchanged pinned chat was returned as changed: %+v", changedByID["same"])
	}

	stale, err := db.GetChat(ctx, "stale")
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}
	if stale.IsPinned || stale.PinnedOrder != 0 {
		t.Fatalf("stale chat remained pinned: %+v", stale)
	}

	updated, err := db.GetChat(ctx, "changed")
	if err != nil {
		t.Fatalf("get changed: %v", err)
	}
	if !updated.IsPinned || updated.PinnedOrder != 350 {
		t.Fatalf("changed chat not reconciled: %+v", updated)
	}

	newPinned, err := db.GetChat(ctx, "new")
	if err != nil {
		t.Fatalf("get new: %v", err)
	}
	if !newPinned.IsPinned || newPinned.PinnedOrder != 400 {
		t.Fatalf("new chat not pinned: %+v", newPinned)
	}
}
