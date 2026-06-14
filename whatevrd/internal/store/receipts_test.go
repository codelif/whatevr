package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func receiptsTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, ctx
}

func saveOutgoingGroupMessage(t *testing.T, db *DB, ctx context.Context, id string) {
	t.Helper()
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        "group@g.us:" + id,
		ChatID:    "group@g.us",
		SenderID:  "me",
		Text:      "hello",
		Timestamp: time.Unix(100, 0),
		Direction: DirectionOutgoing,
		Status:    StatusSent,
		IsGroup:   true,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
}

func TestUpsertMessageReceiptKeepsEarliestAndImpliesDelivered(t *testing.T) {
	db, ctx := receiptsTestDB(t)
	saveOutgoingGroupMessage(t, db, ctx, "msg-1")
	const messageID = "group@g.us:msg-1"

	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "a@s.whatsapp.net", ReceiptKindDelivered, time.Unix(200, 0)); err != nil {
		t.Fatalf("delivered receipt: %v", err)
	}
	// A second, later delivered receipt must not move the timestamp.
	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "a@s.whatsapp.net", ReceiptKindDelivered, time.Unix(300, 0)); err != nil {
		t.Fatalf("repeat delivered receipt: %v", err)
	}
	// Read implies delivered for a participant we never saw deliver.
	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "b@s.whatsapp.net", ReceiptKindRead, time.Unix(400, 0)); err != nil {
		t.Fatalf("read receipt: %v", err)
	}

	receipts, err := db.ListMessageReceipts(ctx, messageID)
	if err != nil {
		t.Fatalf("list receipts: %v", err)
	}
	if len(receipts) != 2 {
		t.Fatalf("expected 2 receipts, got %d", len(receipts))
	}
	byJID := map[string]MessageReceipt{}
	for _, receipt := range receipts {
		byJID[receipt.ParticipantJID] = receipt
	}
	if got := byJID["a@s.whatsapp.net"]; got.DeliveredTs != 200 || got.ReadTs != 0 {
		t.Fatalf("unexpected receipt for a: %+v", got)
	}
	if got := byJID["b@s.whatsapp.net"]; got.DeliveredTs != 400 || got.ReadTs != 400 {
		t.Fatalf("unexpected receipt for b: %+v", got)
	}
}

func TestGroupParticipantsCRUD(t *testing.T) {
	db, ctx := receiptsTestDB(t)
	const chatID = "group@g.us"

	if err := db.ReplaceGroupParticipants(ctx, chatID, []string{"a@s.whatsapp.net", "b@s.whatsapp.net"}); err != nil {
		t.Fatalf("replace participants: %v", err)
	}
	if err := db.AddGroupParticipants(ctx, chatID, []string{"c@s.whatsapp.net"}); err != nil {
		t.Fatalf("add participant: %v", err)
	}
	if err := db.RemoveGroupParticipants(ctx, chatID, []string{"a@s.whatsapp.net"}); err != nil {
		t.Fatalf("remove participant: %v", err)
	}

	participants, err := db.ListGroupParticipants(ctx, chatID)
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	want := []string{"b@s.whatsapp.net", "c@s.whatsapp.net"}
	if len(participants) != len(want) || participants[0] != want[0] || participants[1] != want[1] {
		t.Fatalf("unexpected participants: %v", participants)
	}
}

func TestMarkMessageRevokedClearsContentAndUpdatesSummary(t *testing.T) {
	db, ctx := receiptsTestDB(t)
	saveOutgoingGroupMessage(t, db, ctx, "msg-1")
	const messageID = "group@g.us:msg-1"

	message, chat, changed, err := db.MarkMessageRevoked(ctx, messageID)
	if err != nil {
		t.Fatalf("mark revoked: %v", err)
	}
	if !changed {
		t.Fatal("expected revoke to change the message")
	}
	if !message.IsRevoked || message.Text != "" {
		t.Fatalf("unexpected revoked message: %+v", message)
	}
	if chat.LastMessage != "This message was deleted" {
		t.Fatalf("unexpected chat summary: %q", chat.LastMessage)
	}

	// Idempotent on repeat.
	_, _, changedAgain, err := db.MarkMessageRevoked(ctx, messageID)
	if err != nil {
		t.Fatalf("repeat revoke: %v", err)
	}
	if changedAgain {
		t.Fatal("expected repeat revoke to be a no-op")
	}
}

func TestUpdateMessageTextEditsBodyAndUpdatesSummary(t *testing.T) {
	db, ctx := receiptsTestDB(t)
	saveOutgoingGroupMessage(t, db, ctx, "msg-1")
	const messageID = "group@g.us:msg-1"

	message, chat, changed, err := db.UpdateMessageText(ctx, messageID, "edited hello", nil)
	if err != nil {
		t.Fatalf("update text: %v", err)
	}
	if !changed {
		t.Fatal("expected edit to change the message")
	}
	if !message.IsEdited || message.Text != "edited hello" {
		t.Fatalf("unexpected edited message: %+v", message)
	}
	if chat.LastMessage != "edited hello" {
		t.Fatalf("unexpected chat summary: %q", chat.LastMessage)
	}

	// Idempotent when the text is unchanged.
	_, _, changedAgain, err := db.UpdateMessageText(ctx, messageID, "edited hello", nil)
	if err != nil {
		t.Fatalf("repeat edit: %v", err)
	}
	if changedAgain {
		t.Fatal("expected repeat edit with same text to be a no-op")
	}

	// A second distinct edit still applies.
	updated, _, changed, err := db.UpdateMessageText(ctx, messageID, "edited again", nil)
	if err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if !changed || updated.Text != "edited again" {
		t.Fatalf("unexpected second edit: changed=%v message=%+v", changed, updated)
	}
}

func TestUpdateMessageTextRefusesRevokedMessage(t *testing.T) {
	db, ctx := receiptsTestDB(t)
	saveOutgoingGroupMessage(t, db, ctx, "msg-1")
	const messageID = "group@g.us:msg-1"

	if _, _, _, err := db.MarkMessageRevoked(ctx, messageID); err != nil {
		t.Fatalf("mark revoked: %v", err)
	}

	message, _, changed, err := db.UpdateMessageText(ctx, messageID, "too late", nil)
	if err != nil {
		t.Fatalf("update text on revoked: %v", err)
	}
	if changed {
		t.Fatal("expected editing a revoked message to be a no-op")
	}
	if message.Text != "" || message.IsEdited {
		t.Fatalf("revoked message should stay tombstoned: %+v", message)
	}
}

func TestDeleteMessageForMeRemovesRowAndReceipts(t *testing.T) {
	db, ctx := receiptsTestDB(t)
	saveOutgoingGroupMessage(t, db, ctx, "msg-1")
	const messageID = "group@g.us:msg-1"

	if err := db.UpsertMessageReceipt(ctx, messageID, "group@g.us", "a@s.whatsapp.net", ReceiptKindRead, time.Unix(200, 0)); err != nil {
		t.Fatalf("receipt: %v", err)
	}

	_, _, existed, err := db.DeleteMessageForMe(ctx, messageID)
	if err != nil {
		t.Fatalf("delete for me: %v", err)
	}
	if !existed {
		t.Fatal("expected the message to exist")
	}

	if _, err := db.GetMessage(ctx, messageID); err == nil {
		t.Fatal("expected the message to be gone")
	}
	receipts, err := db.ListMessageReceipts(ctx, messageID)
	if err != nil {
		t.Fatalf("list receipts: %v", err)
	}
	if len(receipts) != 0 {
		t.Fatalf("expected receipts to cascade, got %d", len(receipts))
	}

	_, _, existedAgain, err := db.DeleteMessageForMe(ctx, messageID)
	if err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if existedAgain {
		t.Fatal("expected repeat delete to report missing row")
	}
}
