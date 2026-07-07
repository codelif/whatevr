package protocol

import (
	"context"
	"testing"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

func seedMediaMessage(t *testing.T, db *store.DB, id, chatID string) {
	t.Helper()
	if _, err := db.SaveMediaMessage(context.Background(), store.MediaMessageInput{
		TextMessageInput: store.TextMessageInput{
			ID:        id,
			ChatID:    chatID,
			ChatName:  "Transfer Chat",
			SenderID:  "sender@s.whatsapp.net",
			Timestamp: time.Unix(1_700_000_000, 0),
			Direction: store.DirectionIncoming,
			Status:    store.StatusDelivered,
		},
		MediaKind:     store.MediaKindImage,
		MediaMimeType: "image/jpeg",
		MediaPayload:  []byte("payload"),
	}); err != nil {
		t.Fatalf("seed media message: %v", err)
	}
}

func TestTransfersViewInitialAndLiveProgress(t *testing.T) {
	socketPath, daemon, _ := startChatsTestServer(t)
	daemon.PublishMediaDownloadChanged("m1", "chat-a", true, "", 10, 100)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"transfers"}`)

	upsert := c.expectUpsert(sub, "m1")
	item := upsert["item"].(map[string]any)
	if item["message_id"] != "m1" || item["chat_id"] != "chat-a" || item["direction"] != "download" {
		t.Fatalf("initial transfer item = %v", item)
	}
	if item["received_bytes"] != float64(10) || item["total_bytes"] != float64(100) {
		t.Fatalf("initial transfer bytes = %v", item)
	}
	c.expectReady(sub, true)

	daemon.PublishMediaDownloadChanged("m1", "chat-a", true, "", 50, 100)
	upsert = c.expectUpsert(sub, "m1")
	item = upsert["item"].(map[string]any)
	if item["received_bytes"] != float64(50) || item["total_bytes"] != float64(100) {
		t.Fatalf("updated transfer bytes = %v", item)
	}

	// Non-transfer daemon events must not disturb the transfers view.
	daemon.PublishChatPresence("chat-a", "sender@s.whatsapp.net", true)
	daemon.PublishMediaDownloadChanged("m1", "chat-a", false, "", 0, 100)
	c.expectRemove(sub, "m1")
}

func TestTransfersFailureRemovesTransferAndUpsertsMessageError(t *testing.T) {
	socketPath, daemon, db := startChatsTestServer(t)
	seedMediaMessage(t, db, "chat-a:m1", "chat-a")

	c := dialTest(t, socketPath)
	c.hello()
	messagesSub := c.subscribe(2, `{"view":"messages","chat_id":"chat-a","anchor":"latest"}`)
	initial := c.expectUpsert(messagesSub, "chat-a:m1")
	if media := initial["item"].(map[string]any)["media"].(map[string]any); media["download_error"] != nil {
		t.Fatalf("initial media should have no download_error: %v", media)
	}
	c.expectReady(messagesSub, true)

	transfersSub := c.subscribe(3, `{"view":"transfers"}`)
	c.expectReady(transfersSub, true)

	daemon.PublishMediaDownloadChanged("chat-a:m1", "chat-a", true, "", 1, 10)
	c.expectUpsert(transfersSub, "chat-a:m1")

	updated, err := db.SetMessageMediaDownloadError(context.Background(), "chat-a:m1", "download media: network down")
	if err != nil {
		t.Fatalf("set media download error: %v", err)
	}
	daemon.PublishMediaDownloadChanged("chat-a:m1", "chat-a", false, "download media: network down", 0, 10)
	c.expectRemove(transfersSub, "chat-a:m1")
	daemon.PublishMessageUpdated(app.Message{ID: updated.ID, ChatID: updated.ChatID})

	msg := c.expectUpsert(messagesSub, "chat-a:m1")
	media := msg["item"].(map[string]any)["media"].(map[string]any)
	if media["download_error"] != "download media: network down" {
		t.Fatalf("message media after failure = %v", media)
	}

	updated, err = db.SetMessageMediaDownloadError(context.Background(), "chat-a:m1", "")
	if err != nil {
		t.Fatalf("clear media download error: %v", err)
	}
	daemon.PublishMessageUpdated(app.Message{ID: updated.ID, ChatID: updated.ChatID})
	msg = c.expectUpsert(messagesSub, "chat-a:m1")
	media = msg["item"].(map[string]any)["media"].(map[string]any)
	if _, ok := media["download_error"]; ok {
		t.Fatalf("message media after retry clear = %v", media)
	}
}
