package wa

import (
	"context"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// TestNewsletterSkipsChats locks in that channel posts never materialize as
// chats: they belong to the Channels tab, whose view fetches live from the
// server on every fill.
func TestNewsletterSkipsChats(t *testing.T) {
	client := newMediaIngestClient(t)
	ctx := context.Background()

	evt := mediaIngestEvent("N1", &waE2E.Message{
		Conversation: proto.String("hello channel"),
	})
	evt.Info.Chat = types.JID{User: "120363144038483540", Server: types.NewsletterServer}
	client.handleMessage(ctx, evt, false)

	if chat, err := client.store.GetChat(ctx, "120363144038483540@newsletter"); err == nil {
		t.Fatalf("newsletter materialized as chat %+v; channel posts must never create chats", chat)
	}
}
