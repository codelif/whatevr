package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
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

func TestListChatsForView(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	base := time.Unix(1_700_000_000, 0)
	// Two direct chats and one group; distinct timestamps set recency.
	seed := []struct {
		id      string
		isGroup bool
		offset  time.Duration
	}{
		{"direct-old@s.whatsapp.net", false, 0},
		{"direct-new@s.whatsapp.net", false, 2 * time.Minute},
		{"group-1@g.us", true, time.Minute},
	}
	for _, s := range seed {
		if _, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:        "m-" + s.id,
			ChatID:    s.id,
			Text:      "hi",
			Timestamp: base.Add(s.offset),
			IsGroup:   s.isGroup,
		}); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	ids := func(chats []Chat) []string {
		out := make([]string, len(chats))
		for i, c := range chats {
			out[i] = c.ID
		}
		return out
	}

	// all filter: most recent first.
	all, err := db.ListChatsForView(ctx, ChatListFilter{Kind: ChatFilterAll})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if got := ids(all); len(got) != 3 || got[0] != "direct-new@s.whatsapp.net" || got[1] != "group-1@g.us" || got[2] != "direct-old@s.whatsapp.net" {
		t.Fatalf("all order = %v", got)
	}

	// direct filter excludes the group.
	direct, err := db.ListChatsForView(ctx, ChatListFilter{Kind: ChatFilterDirect})
	if err != nil {
		t.Fatalf("list direct: %v", err)
	}
	if got := ids(direct); len(got) != 2 || got[0] != "direct-new@s.whatsapp.net" || got[1] != "direct-old@s.whatsapp.net" {
		t.Fatalf("direct order = %v", got)
	}

	// groups filter keeps only the group.
	groups, err := db.ListChatsForView(ctx, ChatListFilter{Kind: ChatFilterGroups})
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if got := ids(groups); len(got) != 1 || got[0] != "group-1@g.us" {
		t.Fatalf("groups = %v", got)
	}

	// Pinning lifts a chat above more-recent unpinned ones.
	if _, _, err := db.UpdateChatPinState(ctx, "direct-old@s.whatsapp.net", true, 1); err != nil {
		t.Fatalf("pin: %v", err)
	}
	pinned, err := db.ListChatsForView(ctx, ChatListFilter{Kind: ChatFilterAll})
	if err != nil {
		t.Fatalf("list pinned: %v", err)
	}
	if got := ids(pinned); got[0] != "direct-old@s.whatsapp.net" {
		t.Fatalf("pinned chat not first: %v", got)
	}

	// Archiving segregates: the archived tab shows it, the main list does not.
	if _, _, err := db.UpdateChatArchiveState(ctx, "group-1@g.us", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	main, err := db.ListChatsForView(ctx, ChatListFilter{Kind: ChatFilterAll})
	if err != nil {
		t.Fatalf("list main: %v", err)
	}
	for _, c := range main {
		if c.ID == "group-1@g.us" {
			t.Fatalf("archived chat leaked into main list: %v", ids(main))
		}
	}
	archived, err := db.ListChatsForView(ctx, ChatListFilter{Kind: ChatFilterAll, Archived: true})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if got := ids(archived); len(got) != 1 || got[0] != "group-1@g.us" {
		t.Fatalf("archived tab = %v", got)
	}

	// Limit bounds the window.
	limited, err := db.ListChatsForView(ctx, ChatListFilter{Kind: ChatFilterAll, Limit: 1})
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit not applied: %v", ids(limited))
	}
}

func TestUpdateChatArchiveState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.EnsureChat(ctx, "chat-1", "chat-1", false); err != nil {
		t.Fatalf("ensure chat: %v", err)
	}

	chat, changed, err := db.UpdateChatArchiveState(ctx, "chat-1", true)
	if err != nil {
		t.Fatalf("archive chat: %v", err)
	}
	if !changed || !chat.IsArchived {
		t.Fatalf("expected chat archived, got changed=%v chat=%+v", changed, chat)
	}

	// Idempotent: archiving an already-archived chat reports no change.
	if _, changed, err := db.UpdateChatArchiveState(ctx, "chat-1", true); err != nil {
		t.Fatalf("re-archive chat: %v", err)
	} else if changed {
		t.Fatal("re-archiving an archived chat reported a change")
	}

	chat, changed, err = db.UpdateChatArchiveState(ctx, "chat-1", false)
	if err != nil {
		t.Fatalf("unarchive chat: %v", err)
	}
	if !changed || chat.IsArchived {
		t.Fatalf("expected chat unarchived, got changed=%v chat=%+v", changed, chat)
	}
}

func TestReconcileChatArchives(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, chatID := range []string{"stale", "keep", "new"} {
		if _, err := db.EnsureChat(ctx, chatID, chatID, false); err != nil {
			t.Fatalf("ensure chat %s: %v", chatID, err)
		}
	}
	if _, _, err := db.UpdateChatArchiveState(ctx, "stale", true); err != nil {
		t.Fatalf("archive stale: %v", err)
	}
	if _, _, err := db.UpdateChatArchiveState(ctx, "keep", true); err != nil {
		t.Fatalf("archive keep: %v", err)
	}

	changed, err := db.ReconcileChatArchives(ctx, map[string]struct{}{
		"keep": {},
		"new":  {},
	})
	if err != nil {
		t.Fatalf("reconcile archives: %v", err)
	}

	changedByID := make(map[string]Chat, len(changed))
	for _, chat := range changed {
		changedByID[chat.ID] = chat
	}
	// "stale" must be unarchived, "new" archived; "keep" was already correct.
	if _, ok := changedByID["stale"]; !ok {
		t.Fatalf("expected stale chat to change: %+v", changedByID)
	}
	if _, ok := changedByID["new"]; !ok {
		t.Fatalf("expected new chat to change: %+v", changedByID)
	}
	if _, ok := changedByID["keep"]; ok {
		t.Fatal("unchanged archived chat was returned as changed")
	}

	stale, err := db.GetChat(ctx, "stale")
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}
	if stale.IsArchived {
		t.Fatalf("stale chat remained archived: %+v", stale)
	}
	newChat, err := db.GetChat(ctx, "new")
	if err != nil {
		t.Fatalf("get new: %v", err)
	}
	if !newChat.IsArchived {
		t.Fatalf("new chat was not archived: %+v", newChat)
	}
}

func TestListChatsKeysetPagination(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for i := 1; i <= 5; i++ {
		if _, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:        fmt.Sprintf("chat-%d:msg", i),
			ChatID:    fmt.Sprintf("chat-%d", i),
			ChatName:  fmt.Sprintf("Chat %d", i),
			SenderID:  "sender-1",
			Text:      "hi",
			Timestamp: time.Unix(int64(100*i), 0),
			Direction: DirectionIncoming,
			Status:    StatusDelivered,
		}); err != nil {
			t.Fatalf("save message %d: %v", i, err)
		}
	}

	firstPage, err := db.ListChats(ctx, 2, 0, "")
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(firstPage) != 2 || firstPage[0].ID != "chat-5" || firstPage[1].ID != "chat-4" {
		t.Fatalf("unexpected first page: %+v", firstPage)
	}

	secondPage, err := db.ListChats(ctx, 2, 0, firstPage[1].ID)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(secondPage) != 2 || secondPage[0].ID != "chat-3" || secondPage[1].ID != "chat-2" {
		t.Fatalf("unexpected second page: %+v", secondPage)
	}

	// Unknown cursor falls back to the start of the list.
	fallback, err := db.ListChats(ctx, 1, 0, "missing-chat")
	if err != nil {
		t.Fatalf("fallback page: %v", err)
	}
	if len(fallback) != 1 || fallback[0].ID != "chat-5" {
		t.Fatalf("unexpected fallback page: %+v", fallback)
	}

	if firstPage[0].UpdatedAt <= 0 {
		t.Fatalf("expected updated_at to be stamped, got %d", firstPage[0].UpdatedAt)
	}
}
