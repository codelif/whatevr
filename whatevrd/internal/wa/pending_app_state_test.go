package wa

import (
	"context"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// A LID chat with no PN mapping and no chat row must be parked, not created
// as an orphan @lid row; a later reconcile pass (here: the final one at sync
// settle) applies the parked pin/archive/mute to the chat.
func TestPendingAppStateParksUnresolvedLIDsAndAppliesOnFinal(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	client := &Client{store: db, daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}
	lid := types.NewJID("12345", types.HiddenUserServer)

	resolved, ok := client.resolveAppStateChatJID(ctx, lid)
	if ok {
		t.Fatalf("resolveAppStateChatJID(%s) resolved unexpectedly to %s", lid, resolved)
	}
	client.parkPendingAppState(resolved, func(e *pendingAppStateEntry) {
		e.hasPin, e.pinned, e.pinOrder = true, true, 42
		e.hasArchive, e.archived = true, true
		e.hasMute, e.muted, e.muteEnd = true, true, int64(-1)
	})

	// A non-final pass must keep the entry parked (still unresolved).
	client.reconcilePendingAppState(ctx, false)
	if _, err := db.GetChat(ctx, lid.String()); err == nil {
		t.Fatal("non-final reconcile created a chat row for an unresolved LID")
	}
	client.pendingAppStateMu.Lock()
	parked := len(client.pendingAppState)
	client.pendingAppStateMu.Unlock()
	if parked != 1 {
		t.Fatalf("parked entries after non-final pass = %d, want 1", parked)
	}

	// The final pass applies to the LID chat itself (genuine LID-only contact).
	client.reconcilePendingAppState(ctx, true)
	chat, err := db.GetChat(ctx, lid.String())
	if err != nil {
		t.Fatalf("final reconcile did not create the chat: %v", err)
	}
	if !chat.IsPinned || chat.PinnedOrder != 42 {
		t.Fatalf("pin state = %v/%d, want pinned with order 42", chat.IsPinned, chat.PinnedOrder)
	}
	if !chat.IsArchived {
		t.Fatal("chat is not archived")
	}
	if !chat.IsMuted || chat.MuteEndTimestamp != -1 {
		t.Fatalf("mute state = %v/%d, want muted forever (-1)", chat.IsMuted, chat.MuteEndTimestamp)
	}

	client.pendingAppStateMu.Lock()
	remaining := len(client.pendingAppState)
	client.pendingAppStateMu.Unlock()
	if remaining != 0 {
		t.Fatalf("parked entries after final pass = %d, want 0", remaining)
	}
}

// State parked for a LID that already has a chat row applies immediately:
// resolveAppStateChatJID treats an existing @lid chat as canonical.
func TestResolveAppStateChatJIDUsesExistingLIDChat(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	client := &Client{store: db, daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}
	lid := types.NewJID("67890", types.HiddenUserServer)
	if _, err := db.EnsureChat(ctx, lid.String(), "LID-only contact", false); err != nil {
		t.Fatalf("ensure chat: %v", err)
	}

	if _, ok := client.resolveAppStateChatJID(ctx, lid); !ok {
		t.Fatal("existing LID chat was not treated as resolvable")
	}
}
