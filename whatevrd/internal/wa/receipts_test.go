package wa

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

type testLIDStore struct {
	lidByPN map[string]types.JID
	pnByLID map[string]types.JID
}

func (s *testLIDStore) PutManyLIDMappings(ctx context.Context, mappings []waStore.LIDMapping) error {
	for _, mapping := range mappings {
		if err := s.PutLIDMapping(ctx, mapping.LID, mapping.PN); err != nil {
			return err
		}
	}
	return nil
}

func (s *testLIDStore) PutLIDMapping(_ context.Context, lid, pn types.JID) error {
	if s.lidByPN == nil {
		s.lidByPN = make(map[string]types.JID)
	}
	if s.pnByLID == nil {
		s.pnByLID = make(map[string]types.JID)
	}
	s.lidByPN[pn.String()] = lid
	s.pnByLID[lid.String()] = pn
	return nil
}

func (s *testLIDStore) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	return s.pnByLID[lid.String()], nil
}

func (s *testLIDStore) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	return s.lidByPN[pn.String()], nil
}

func (s *testLIDStore) GetManyLIDsForPNs(_ context.Context, pns []types.JID) (map[types.JID]types.JID, error) {
	out := make(map[types.JID]types.JID, len(pns))
	for _, pn := range pns {
		if lid, ok := s.lidByPN[pn.String()]; ok {
			out[pn] = lid
		}
	}
	return out, nil
}

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

func TestGetMessageInfoUsesParticipantAvatarFromLIDAlias(t *testing.T) {
	c, db := newReceiptTestClient(t)
	ctx := context.Background()
	messageID := seedGroupMessage(t, db, appstore.StatusSent)
	pn := types.NewJID("1111", types.DefaultUserServer)
	lid := types.NewJID("aaaa", types.HiddenUserServer)

	lids := &testLIDStore{}
	if err := lids.PutLIDMapping(ctx, lid, pn); err != nil {
		t.Fatalf("put lid mapping: %v", err)
	}
	c.client = &whatsmeow.Client{Store: &waStore.Device{LIDs: lids}}

	if err := db.ReplaceGroupParticipants(ctx, "group@g.us", []string{pn.String()}); err != nil {
		t.Fatalf("participants: %v", err)
	}
	c.markGroupParticipantsFresh("group@g.us")
	if err := db.UpdateSenderName(ctx, lid.String(), "Alice"); err != nil {
		t.Fatalf("sender name: %v", err)
	}
	if err := db.UpdateSenderAvatar(ctx, lid.String(), "pic-1", "/tmp/alice.jpg"); err != nil {
		t.Fatalf("sender avatar: %v", err)
	}

	info, err := c.GetMessageInfo(ctx, messageID)
	if err != nil {
		t.Fatalf("message info: %v", err)
	}
	if len(info.Receipts) != 1 {
		t.Fatalf("receipt count = %d, want 1", len(info.Receipts))
	}
	receipt := info.Receipts[0]
	if receipt.JID != pn.String() {
		t.Fatalf("receipt jid = %q, want %q", receipt.JID, pn.String())
	}
	if receipt.DisplayName != "Alice" || receipt.AvatarLocalPath != "/tmp/alice.jpg" {
		t.Fatalf("receipt display = %q, avatar = %q", receipt.DisplayName, receipt.AvatarLocalPath)
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

func TestHandleEditMessageUpdatesBody(t *testing.T) {
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
			ID:            "edit-1",
		},
		Message: &waE2E.Message{
			ProtocolMessage: &waE2E.ProtocolMessage{
				Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
				Key: &waCommon.MessageKey{
					RemoteJID: proto.String("group@g.us"),
					ID:        proto.String("msg-1"),
				},
				EditedMessage: &waE2E.Message{Conversation: proto.String("edited body")},
			},
		},
	}

	if !c.handleEditMessage(ctx, evt, false) {
		t.Fatal("expected the edit protocol message to be intercepted")
	}

	message, err := db.GetMessage(ctx, messageID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if !message.IsEdited || message.Text != "edited body" {
		t.Fatalf("expected edited body, got %+v", message)
	}

	// A regular text message is not intercepted as an edit.
	plain := &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: chatJID}, ID: "plain-1"},
		Message: &waE2E.Message{Conversation: proto.String("hi")},
	}
	if c.handleEditMessage(ctx, plain, false) {
		t.Fatal("plain message must not be treated as an edit")
	}
}

// Newer WhatsApp clients seal an edit in a SecretEncryptedMessage instead of
// sending a bare protocol message. Such an event must never reach the ingest
// path — before it was recognised the edit was silently dropped, leaving the
// message showing its pre-edit body with no edited mark. Opening the envelope
// needs the target message's secret (so it is exercised live), but the routing
// either side of it is not: an unopenable edit is still swallowed, and an
// envelope carrying some other secret type is not an edit at all.
func TestSecretEncryptedEditIsRoutedAsAnEdit(t *testing.T) {
	c, _ := newReceiptTestClient(t)
	ctx := context.Background()

	chatJID, err := types.ParseJID("group@g.us")
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	sealed := func(encType waE2E.SecretEncryptedMessage_SecretEncType) *events.Message {
		return &events.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{Chat: chatJID},
				ID:            "edit-1",
			},
			Message: &waE2E.Message{
				SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
					TargetMessageKey: &waCommon.MessageKey{
						RemoteJID: proto.String("group@g.us"),
						ID:        proto.String("msg-1"),
					},
					SecretEncType: encType.Enum(),
				},
			},
		}
	}

	if !c.handleEditMessage(ctx, sealed(waE2E.SecretEncryptedMessage_MESSAGE_EDIT), false) {
		t.Fatal("a sealed edit must be intercepted even when it cannot be opened")
	}
	if c.handleEditMessage(ctx, sealed(waE2E.SecretEncryptedMessage_POLL_EDIT), false) {
		t.Fatal("a sealed poll edit must not be treated as a message edit")
	}
}

func TestEditMessageRejectsIneligibleMessages(t *testing.T) {
	ctx := context.Background()

	// Out-of-window: seedGroupMessage timestamps at 1970, far past EditWindow.
	t.Run("expired window", func(t *testing.T) {
		c, db := newReceiptTestClient(t)
		messageID := seedGroupMessage(t, db, appstore.StatusSent)
		_, err := c.EditMessage(ctx, messageID, "too late")
		if ce, ok := app.AsCommandError(err); !ok || ce.Kind != app.CommandErrorExpired {
			t.Fatalf("expected CommandErrorExpired, got %v", err)
		}
	})

	t.Run("not our message", func(t *testing.T) {
		c, db := newReceiptTestClient(t)
		const messageID = "group@g.us:in-1"
		if _, err := db.SaveTextMessage(ctx, appstore.TextMessageInput{
			ID:        messageID,
			ChatID:    "group@g.us",
			SenderID:  "someone@s.whatsapp.net",
			Text:      "hi",
			Timestamp: time.Now(),
			Direction: appstore.DirectionIncoming,
			Status:    appstore.StatusDelivered,
			IsGroup:   true,
		}); err != nil {
			t.Fatalf("save incoming: %v", err)
		}
		_, err := c.EditMessage(ctx, messageID, "nope")
		if ce, ok := app.AsCommandError(err); !ok || ce.Kind != app.CommandErrorRejected {
			t.Fatalf("expected CommandErrorRejected, got %v", err)
		}
	})

	t.Run("revoked message", func(t *testing.T) {
		c, db := newReceiptTestClient(t)
		const messageID = "group@g.us:fresh-1"
		if _, err := db.SaveTextMessage(ctx, appstore.TextMessageInput{
			ID:        messageID,
			ChatID:    "group@g.us",
			SenderID:  "me",
			Text:      "hello",
			Timestamp: time.Now(),
			Direction: appstore.DirectionOutgoing,
			Status:    appstore.StatusSent,
			IsGroup:   true,
		}); err != nil {
			t.Fatalf("save outgoing: %v", err)
		}
		if _, _, _, err := db.MarkMessageRevoked(ctx, messageID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		_, err := c.EditMessage(ctx, messageID, "nope")
		if ce, ok := app.AsCommandError(err); !ok || ce.Kind != app.CommandErrorRejected {
			t.Fatalf("expected CommandErrorRejected, got %v", err)
		}
	})
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
