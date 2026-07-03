package wa

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	waSyncAction "go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func newMarkReadTestClient(t *testing.T) (*Client, *appstore.DB) {
	t.Helper()
	db, err := appstore.Open(context.Background(), filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &Client{store: db, daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}, db
}

func seedUnreadMessages(t *testing.T, db *appstore.DB, chatID string, timestamps ...int64) {
	t.Helper()
	for i, ts := range timestamps {
		if _, err := db.SaveTextMessage(context.Background(), appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, types.MessageID(string(rune('a'+i)))),
			ChatID:      chatID,
			ChatName:    "Test Chat",
			SenderID:    "peer",
			Text:        "hello",
			Timestamp:   time.Unix(ts, 0),
			Direction:   appstore.DirectionIncoming,
			Status:      appstore.StatusDelivered,
			CountUnread: true,
		}); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}
}

// Reading a chat on the phone emits a MarkChatAsRead app-state action; the
// handler must clear the badge up to the action's horizon, and a mark-unread
// action must raise the dot-style badge again.
func TestHandleMarkChatAsReadClearsAndRaisesUnread(t *testing.T) {
	ctx := context.Background()
	client, db := newMarkReadTestClient(t)
	jid := types.NewJID("12345", types.DefaultUserServer)
	chatID := jid.String()
	seedUnreadMessages(t, db, chatID, 100, 200)

	client.handleMarkChatAsReadEvent(ctx, &events.MarkChatAsRead{
		JID:       jid,
		Timestamp: time.Unix(300, 0),
		Action: &waSyncAction.MarkChatAsReadAction{
			Read: proto.Bool(true),
			MessageRange: &waSyncAction.SyncActionMessageRange{
				LastMessageTimestamp: proto.Int64(250),
			},
		},
	})

	chat, err := db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("unread after mark-read = %d, want 0", chat.UnreadCount)
	}

	client.handleMarkChatAsReadEvent(ctx, &events.MarkChatAsRead{
		JID:       jid,
		Timestamp: time.Unix(310, 0),
		Action:    &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(false)},
	})

	chat, err = db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat after mark-unread: %v", err)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("unread after mark-unread = %d, want 1", chat.UnreadCount)
	}
}

// A stale action (horizon before newer messages) must not swallow unread
// messages that arrived after the phone's mark-read.
func TestHandleMarkChatAsReadRespectsHorizon(t *testing.T) {
	ctx := context.Background()
	client, db := newMarkReadTestClient(t)
	jid := types.NewJID("12345", types.DefaultUserServer)
	chatID := jid.String()
	seedUnreadMessages(t, db, chatID, 100, 200, 300)

	client.handleMarkChatAsReadEvent(ctx, &events.MarkChatAsRead{
		JID:       jid,
		Timestamp: time.Unix(220, 0),
		Action: &waSyncAction.MarkChatAsReadAction{
			Read: proto.Bool(true),
			MessageRange: &waSyncAction.SyncActionMessageRange{
				LastMessageTimestamp: proto.Int64(220),
			},
		},
	})

	chat, err := db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("unread after bounded mark-read = %d, want 1", chat.UnreadCount)
	}
}

// Mark-read for a chat we do not know must not create a chat row.
func TestHandleMarkChatAsReadIgnoresUnknownChat(t *testing.T) {
	ctx := context.Background()
	client, db := newMarkReadTestClient(t)
	jid := types.NewJID("98765", types.DefaultUserServer)

	client.handleMarkChatAsReadEvent(ctx, &events.MarkChatAsRead{
		JID:       jid,
		Timestamp: time.Now(),
		Action:    &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(true)},
	})

	if _, err := db.GetChat(ctx, jid.String()); err == nil {
		t.Fatal("mark-read created a chat row for an unknown chat")
	}
}

// A read-self receipt (specific messages read on the phone) clears exactly
// those messages from the badge.
func TestHandleReceiptReadSelfClearsUnread(t *testing.T) {
	client, db := newMarkReadTestClient(t)
	jid := types.NewJID("12345", types.DefaultUserServer)
	chatID := jid.String()
	seedUnreadMessages(t, db, chatID, 100, 200)

	client.handleReceipt(&events.Receipt{
		MessageSource: types.MessageSource{Chat: jid},
		MessageIDs:    []types.MessageID{"a"},
		Timestamp:     time.Unix(300, 0),
		Type:          types.ReceiptTypeReadSelf,
	}, false)

	chat, err := db.GetChat(context.Background(), chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("unread after read-self receipt = %d, want 1", chat.UnreadCount)
	}
}

// WhatsApp may report messages read on the phone as a normal read receipt from
// our account rather than as read-self. Those still clear local unread state.
func TestHandleReceiptSelfNormalReadClearsUnread(t *testing.T) {
	client, db := newMarkReadTestClient(t)
	groupJID := types.NewJID("120363401126460521", types.GroupServer)
	chatID := groupJID.String()
	seedUnreadMessages(t, db, chatID, 100, 200)

	client.handleReceipt(&events.Receipt{
		MessageSource: types.MessageSource{
			Chat:     groupJID,
			Sender:   types.NewJID("30413133201647", types.HiddenUserServer),
			IsFromMe: true,
			IsGroup:  true,
		},
		MessageSender: types.NewJID("202963679203505", types.HiddenUserServer),
		MessageIDs:    []types.MessageID{"a"},
		Timestamp:     time.Unix(300, 0),
		Type:          types.ReceiptTypeRead,
	}, false)

	chat, err := db.GetChat(context.Background(), chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("unread after self normal read receipt = %d, want 1", chat.UnreadCount)
	}
	message, err := db.GetMessage(context.Background(), internalMessageIDForChat(chatID, "a"))
	if err != nil {
		t.Fatalf("get read message: %v", err)
	}
	if !message.IsRead {
		t.Fatal("self normal read receipt did not mark message read")
	}
}

func TestHandleReceiptPeerReadDoesNotClearUnread(t *testing.T) {
	client, db := newMarkReadTestClient(t)
	groupJID := types.NewJID("120363401126460521", types.GroupServer)
	chatID := groupJID.String()
	seedUnreadMessages(t, db, chatID, 100, 200)

	client.handleReceipt(&events.Receipt{
		MessageSource: types.MessageSource{
			Chat:    groupJID,
			Sender:  types.NewJID("5871320977495", types.HiddenUserServer),
			IsGroup: true,
		},
		MessageIDs: []types.MessageID{"a"},
		Timestamp:  time.Unix(300, 0),
		Type:       types.ReceiptTypeRead,
	}, false)

	chat, err := db.GetChat(context.Background(), chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 2 {
		t.Fatalf("unread after peer read receipt = %d, want 2", chat.UnreadCount)
	}
}

// An unresolved LID parks the mark-read action; the final reconcile pass
// applies it to the LID chat itself (genuine LID-only contact).
func TestHandleMarkChatAsReadParksUnresolvedLID(t *testing.T) {
	ctx := context.Background()
	client, db := newMarkReadTestClient(t)
	lid := types.NewJID("55555", types.HiddenUserServer)

	client.handleMarkChatAsReadEvent(ctx, &events.MarkChatAsRead{
		JID:       lid,
		Timestamp: time.Unix(100, 0),
		Action:    &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(false)},
	})

	if _, err := db.GetChat(ctx, lid.String()); err == nil {
		t.Fatal("unresolved LID mark-read touched the store before reconcile")
	}
	client.pendingAppStateMu.Lock()
	parked := len(client.pendingAppState)
	client.pendingAppStateMu.Unlock()
	if parked != 1 {
		t.Fatalf("parked entries = %d, want 1", parked)
	}

	client.reconcilePendingAppState(ctx, true)

	chat, err := db.GetChat(ctx, lid.String())
	if err != nil {
		t.Fatalf("final reconcile did not apply parked mark-unread: %v", err)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("unread after reconciled mark-unread = %d, want 1", chat.UnreadCount)
	}
}

func TestReconcileMarkReadFromEventsAppliesMarkUnread(t *testing.T) {
	ctx := context.Background()
	client, db := newMarkReadTestClient(t)
	jid := types.NewJID("12345", types.DefaultUserServer)
	chatID := jid.String()
	if _, err := db.EnsureChat(ctx, chatID, "Test Chat", false); err != nil {
		t.Fatalf("ensure chat: %v", err)
	}
	daemonEvents, unsubscribe := client.daemon.SubscribeDaemonEvents()
	t.Cleanup(unsubscribe)

	client.reconcileMarkReadFromEvents(ctx, []any{&events.MarkChatAsRead{
		JID:       jid,
		Timestamp: time.Unix(300, 0),
		Action:    &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(false)},
	}})

	chat, err := db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("unread after reconciled full-sync mark-unread = %d, want 1", chat.UnreadCount)
	}

	for deadline := time.After(time.Second); ; {
		select {
		case evt := <-daemonEvents:
			if evt.Kind == app.DaemonEventChatUpdated && evt.Chat.ID == chatID && evt.Chat.UnreadCount == 1 {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for reconciled mark-unread ChatUpdated")
		}
	}
}
