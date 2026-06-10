package wa

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func newReceiptTestClient(t *testing.T) (*Client, *appstore.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &Client{
		store:  db,
		daemon: app.NewDaemon(app.Paths{}),
		log:    waLog.Noop,
	}, db
}

func seedGroupMessage(t *testing.T, db *appstore.DB, status string) string {
	t.Helper()
	const messageID = "group@g.us:msg-1"
	if _, err := db.SaveTextMessage(context.Background(), appstore.TextMessageInput{
		ID:        messageID,
		ChatID:    "group@g.us",
		SenderID:  "me",
		Text:      "hello",
		Timestamp: time.Unix(100, 0),
		Direction: appstore.DirectionOutgoing,
		Status:    status,
		IsGroup:   true,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
	return messageID
}

func TestAggregateGroupStatusRequiresAllMembers(t *testing.T) {
	c, db := newReceiptTestClient(t)
	ctx := context.Background()
	messageID := seedGroupMessage(t, db, appstore.StatusSent)
	participants := []string{"a@s.whatsapp.net", "b@s.whatsapp.net"}

	// One member delivered: not enough for the delivered tick.
	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "a@s.whatsapp.net", appstore.ReceiptKindDelivered, time.Unix(200, 0)); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	status, err := c.aggregateGroupStatus(ctx, messageID, participants)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if status != "" {
		t.Fatalf("aggregate after one delivery = %q, want none", status)
	}

	// Second member delivered: now everyone has it.
	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "b@s.whatsapp.net", appstore.ReceiptKindDelivered, time.Unix(210, 0)); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	status, err = c.aggregateGroupStatus(ctx, messageID, participants)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if status != appstore.StatusDelivered {
		t.Fatalf("aggregate after all deliveries = %q, want delivered", status)
	}

	// One member read: still only delivered overall.
	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "a@s.whatsapp.net", appstore.ReceiptKindRead, time.Unix(220, 0)); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	status, err = c.aggregateGroupStatus(ctx, messageID, participants)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if status != appstore.StatusDelivered {
		t.Fatalf("aggregate after one read = %q, want delivered", status)
	}

	// Everyone read: blue ticks.
	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "b@s.whatsapp.net", appstore.ReceiptKindRead, time.Unix(230, 0)); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	status, err = c.aggregateGroupStatus(ctx, messageID, participants)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if status != appstore.StatusRead {
		t.Fatalf("aggregate after all reads = %q, want read", status)
	}
}

func TestAggregateGroupStatusPlayedCountsAsRead(t *testing.T) {
	c, db := newReceiptTestClient(t)
	ctx := context.Background()
	messageID := seedGroupMessage(t, db, appstore.StatusSent)

	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "a@s.whatsapp.net", appstore.ReceiptKindPlayed, time.Unix(200, 0)); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	status, err := c.aggregateGroupStatus(ctx, messageID, []string{"a@s.whatsapp.net"})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if status != appstore.StatusRead {
		t.Fatalf("aggregate after played = %q, want read", status)
	}
}

func TestHandleRevokeMessageTombstonesTarget(t *testing.T) {
	c, db := newReceiptTestClient(t)
	ctx := context.Background()
	messageID := seedGroupMessage(t, db, appstore.StatusSent)

	chatJID, err := types.ParseJID("group@g.us")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chatJID},
			ID:            "revoke-1",
		},
		Message: &waE2E.Message{
			ProtocolMessage: &waE2E.ProtocolMessage{
				Type: waE2E.ProtocolMessage_REVOKE.Enum(),
				Key: &waCommon.MessageKey{
					RemoteJID: proto.String("group@g.us"),
					ID:        proto.String("msg-1"),
				},
			},
		},
	}

	if !c.handleRevokeMessage(ctx, evt, false) {
		t.Fatal("expected the revoke protocol message to be intercepted")
	}

	message, err := db.GetMessage(ctx, messageID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if !message.IsRevoked || message.Text != "" {
		t.Fatalf("expected tombstone, got %+v", message)
	}

	// A regular text message is not intercepted.
	plain := &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: chatJID}, ID: "plain-1"},
		Message: &waE2E.Message{Conversation: proto.String("hi")},
	}
	if c.handleRevokeMessage(ctx, plain, false) {
		t.Fatal("plain message must not be treated as a revoke")
	}
}

func TestUpdateMessageStatusStaysMonotonicWithAggregate(t *testing.T) {
	_, db := newReceiptTestClient(t)
	ctx := context.Background()
	messageID := seedGroupMessage(t, db, appstore.StatusRead)

	// A late "delivered" aggregate must never demote an already-read message.
	message, changed, err := db.UpdateMessageStatus(ctx, messageID, appstore.StatusDelivered)
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if changed || message.Status != appstore.StatusRead {
		t.Fatalf("status regressed: changed=%v status=%q", changed, message.Status)
	}
}
