package wa

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// Reactions stored without a sender name (pre-fix history syncs) get one
// resolved on the read path and persisted, so they heal permanently.
func TestFillReactionSenderNamesResolvesAndPersists(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	client := &Client{store: db, daemon: app.NewDaemon(app.Paths{}), log: waLog.Noop}

	const chatID = "15551234567@s.whatsapp.net"
	messageID := internalMessageIDForChat(chatID, "msg-1")
	if _, err := db.SaveTextMessage(ctx, appstore.TextMessageInput{
		ID:        messageID,
		ChatID:    chatID,
		SenderID:  "me",
		Text:      "hello",
		Timestamp: time.Unix(100, 0),
		Direction: appstore.DirectionOutgoing,
		Status:    appstore.StatusSent,
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if _, _, _, err := db.SaveReaction(ctx, messageID, chatID, "", "🔥", 150, false); err != nil {
		t.Fatalf("seed nameless reaction: %v", err)
	}

	message, err := db.GetMessage(ctx, messageID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if len(message.Reactions) != 1 || message.Reactions[0].SenderName != "" {
		t.Fatalf("seed reaction state unexpected: %+v", message.Reactions)
	}

	filled := client.FillReactionSenderNames(ctx, []appstore.Message{message})
	// Without a contact store the resolver still produces the formatted phone
	// number — anything non-empty replaces the "Someone" placeholder.
	if got := filled[0].Reactions[0].SenderName; got == "" {
		t.Fatal("FillReactionSenderNames left the sender name empty")
	}

	reloaded, err := db.GetMessage(ctx, messageID)
	if err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if reloaded.Reactions[0].SenderName != filled[0].Reactions[0].SenderName {
		t.Fatalf("resolved name was not persisted: db=%q filled=%q",
			reloaded.Reactions[0].SenderName, filled[0].Reactions[0].SenderName)
	}

	// Own reactions and already-named reactions are left alone.
	if _, _, _, err := db.SaveReaction(ctx, messageID, "me", "", "❤", 160, true); err != nil {
		t.Fatalf("seed own reaction: %v", err)
	}
	message, err = db.GetMessage(ctx, messageID)
	if err != nil {
		t.Fatalf("get message again: %v", err)
	}
	filled = client.FillReactionSenderNames(ctx, []appstore.Message{message})
	for _, reaction := range filled[0].Reactions {
		if reaction.FromMe && reaction.SenderName != "" {
			t.Fatalf("own reaction gained a sender name: %+v", reaction)
		}
	}
}
