package wa

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

type fakeEventBuffer struct {
	store.EventBuffer
	format  string
	payload []byte
	err     error
	calls   int
}

func (f *fakeEventBuffer) GetOutgoingEvent(ctx context.Context, chatJID, altChatJID types.JID, id types.MessageID) (string, []byte, error) {
	f.calls++
	return f.format, f.payload, f.err
}

func newRetryFallbackTest(t *testing.T) (*retryFallbackBuffer, *fakeEventBuffer, *appstore.DB) {
	t.Helper()
	db, err := appstore.Open(context.Background(), filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	client := &Client{store: db, daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}
	inner := &fakeEventBuffer{err: sql.ErrNoRows}
	return &retryFallbackBuffer{EventBuffer: inner, client: client}, inner, db
}

func saveOutgoingText(t *testing.T, db *appstore.DB, chatID string, externalID types.MessageID, text string, replyTo appstore.MessageReply) appstore.Message {
	t.Helper()
	saved, err := db.SaveTextMessage(context.Background(), appstore.TextMessageInput{
		ID:        internalMessageIDForChat(chatID, externalID),
		ChatID:    chatID,
		SenderID:  "me",
		Text:      text,
		Timestamp: time.Now(),
		Direction: appstore.DirectionOutgoing,
		Status:    appstore.StatusSent,
		ReplyTo:   replyTo,
	})
	if err != nil {
		t.Fatalf("save text message: %v", err)
	}
	return saved.Message
}

func decodeWireMessage(t *testing.T, format string, payload []byte) *waE2E.Message {
	t.Helper()
	if format != "wa" {
		t.Fatalf("expected format wa, got %q", format)
	}
	msg := &waE2E.Message{}
	if err := proto.Unmarshal(payload, msg); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return msg
}

func TestRetryFallbackRebuildsPlainText(t *testing.T) {
	buf, _, db := newRetryFallbackTest(t)
	chat := types.NewJID("15550001111", types.DefaultUserServer)
	saveOutgoingText(t, db, chat.String(), "WAMID1", "hello again", appstore.MessageReply{})

	format, payload, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "WAMID1")
	if err != nil {
		t.Fatalf("expected fallback hit, got %v", err)
	}
	msg := decodeWireMessage(t, format, payload)
	if msg.GetConversation() != "hello again" {
		t.Fatalf("expected plain Conversation text, got %v", msg)
	}
}

func TestRetryFallbackRebuildsReplyAsExtendedText(t *testing.T) {
	buf, _, db := newRetryFallbackTest(t)
	chat := types.NewJID("15550001111", types.DefaultUserServer)
	original := saveOutgoingText(t, db, chat.String(), "ORIG", "the original", appstore.MessageReply{})
	saveOutgoingText(t, db, chat.String(), "WAMID2", "the reply", appstore.MessageReply{
		MessageID: original.ID,
		SenderID:  "me",
		Text:      original.Text,
	})

	format, payload, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "WAMID2")
	if err != nil {
		t.Fatalf("expected fallback hit, got %v", err)
	}
	msg := decodeWireMessage(t, format, payload)
	ext := msg.GetExtendedTextMessage()
	if ext.GetText() != "the reply" {
		t.Fatalf("expected extended text reply, got %v", msg)
	}
	if ext.GetContextInfo().GetStanzaID() != "ORIG" {
		t.Fatalf("expected quoted stanza ORIG, got %q", ext.GetContextInfo().GetStanzaID())
	}
}

func TestRetryFallbackRebuildsImageFromPayload(t *testing.T) {
	buf, _, db := newRetryFallbackTest(t)
	chat := types.NewJID("15550001111", types.DefaultUserServer)
	id := internalMessageIDForChat(chat.String(), "WAMID3")
	if _, err := db.SaveMediaMessage(context.Background(), appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:        id,
			ChatID:    chat.String(),
			SenderID:  "me",
			Text:      "a caption",
			Timestamp: time.Now(),
			Direction: appstore.DirectionOutgoing,
			Status:    appstore.StatusSent,
		},
		MediaKind:     appstore.MediaKindImage,
		MediaMimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("save media message: %v", err)
	}
	sent := &waE2E.ImageMessage{
		Caption:    proto.String("a caption"),
		Mimetype:   proto.String("image/jpeg"),
		URL:        proto.String("https://example.invalid/img"),
		DirectPath: proto.String("/v/img"),
		MediaKey:   []byte{1, 2, 3},
	}
	payload, err := proto.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal image: %v", err)
	}
	if _, err := db.UpdateMessageMediaPayload(context.Background(), id, payload); err != nil {
		t.Fatalf("persist payload: %v", err)
	}

	format, encoded, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "WAMID3")
	if err != nil {
		t.Fatalf("expected fallback hit, got %v", err)
	}
	msg := decodeWireMessage(t, format, encoded)
	img := msg.GetImageMessage()
	if img.GetDirectPath() != "/v/img" || img.GetCaption() != "a caption" {
		t.Fatalf("expected rebuilt image with media keys and caption, got %v", msg)
	}
}

func TestRetryFallbackRebuildsStickerFromPayload(t *testing.T) {
	buf, _, db := newRetryFallbackTest(t)
	chat := types.NewJID("15550001111", types.DefaultUserServer)
	id := internalMessageIDForChat(chat.String(), "WAMID4")
	sent := &waE2E.StickerMessage{
		Mimetype:   proto.String("image/webp"),
		URL:        proto.String("https://example.invalid/stk"),
		DirectPath: proto.String("/v/stk"),
		MediaKey:   []byte{4, 5, 6},
	}
	payload, err := proto.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal sticker: %v", err)
	}
	if _, err := db.SaveMediaMessage(context.Background(), appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:        id,
			ChatID:    chat.String(),
			SenderID:  "me",
			Timestamp: time.Now(),
			Direction: appstore.DirectionOutgoing,
			Status:    appstore.StatusSent,
		},
		MediaKind:     appstore.MediaKindSticker,
		MediaMimeType: "image/webp",
		MediaCacheKey: "cachekey",
		MediaPayload:  payload,
	}); err != nil {
		t.Fatalf("save sticker message: %v", err)
	}

	format, encoded, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "WAMID4")
	if err != nil {
		t.Fatalf("expected fallback hit, got %v", err)
	}
	msg := decodeWireMessage(t, format, encoded)
	if msg.GetStickerMessage().GetDirectPath() != "/v/stk" {
		t.Fatalf("expected rebuilt sticker, got %v", msg)
	}
}

func TestRetryFallbackResolvesAlternateChatJID(t *testing.T) {
	buf, _, db := newRetryFallbackTest(t)
	pn := types.NewJID("15550001111", types.DefaultUserServer)
	lid := types.NewJID("98765432101234", types.HiddenUserServer)
	saveOutgoingText(t, db, pn.String(), "WAMID5", "via alt jid", appstore.MessageReply{})

	// The receipt names the LID chat; the store row lives under the PN chat,
	// reachable through the alternate JID.
	format, payload, err := buf.GetOutgoingEvent(context.Background(), lid, pn, "WAMID5")
	if err != nil {
		t.Fatalf("expected alt-jid fallback hit, got %v", err)
	}
	if decodeWireMessage(t, format, payload).GetConversation() != "via alt jid" {
		t.Fatal("expected text rebuilt from the alternate chat JID")
	}
}

func TestRetryFallbackMisses(t *testing.T) {
	buf, _, db := newRetryFallbackTest(t)
	chat := types.NewJID("15550001111", types.DefaultUserServer)

	// Unknown id.
	if _, _, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "NOPE"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows for unknown id, got %v", err)
	}

	// Incoming messages are never retry-answered.
	if _, err := db.SaveTextMessage(context.Background(), appstore.TextMessageInput{
		ID:        internalMessageIDForChat(chat.String(), "IN1"),
		ChatID:    chat.String(),
		SenderID:  chat.String(),
		Text:      "from the peer",
		Timestamp: time.Now(),
		Direction: appstore.DirectionIncoming,
		Status:    appstore.StatusSent,
	}); err != nil {
		t.Fatalf("save incoming message: %v", err)
	}
	if _, _, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "IN1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows for incoming message, got %v", err)
	}
}

func TestRetryFallbackInnerHitShortCircuits(t *testing.T) {
	buf, inner, db := newRetryFallbackTest(t)
	chat := types.NewJID("15550001111", types.DefaultUserServer)
	saveOutgoingText(t, db, chat.String(), "WAMID6", "store copy", appstore.MessageReply{})

	inner.format = "wa"
	inner.payload = []byte{9, 9, 9}
	inner.err = nil

	format, payload, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "WAMID6")
	if err != nil || format != "wa" || len(payload) != 3 {
		t.Fatalf("expected the inner buffer's payload untouched, got %q/%v/%v", format, payload, err)
	}

	// A real storage failure must surface, not fall back.
	inner.err = errors.New("disk on fire")
	if _, _, err := buf.GetOutgoingEvent(context.Background(), chat, types.EmptyJID, "WAMID6"); err == nil || err.Error() != "disk on fire" {
		t.Fatalf("expected inner storage failure surfaced, got %v", err)
	}
}
