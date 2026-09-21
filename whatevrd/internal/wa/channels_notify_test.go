package wa

import (
	"context"
	"sync"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	waEvents "go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	"whatevrd/internal/notify"
	appstore "whatevrd/internal/store"
)

type fakeNotifier struct {
	mu       sync.Mutex
	messages []app.Message
	chats    []app.Chat
}

func (f *fakeNotifier) NotifyMessage(_ context.Context, message app.Message, chat app.Chat, _ notify.Options) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	f.chats = append(f.chats, chat)
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

// TestNotifyChannelPost fires for fresh posts on unmuted channels and stays
// silent for muted channels and stale posts.
func TestNotifyChannelPost(t *testing.T) {
	newClient := func(t *testing.T) (*Client, *fakeNotifier) {
		c := newMediaIngestClient(t)
		n := &fakeNotifier{}
		c.notifier = n
		return c, n
	}
	newEvent := func(text string, at time.Time) *waEvents.Message {
		return &waEvents.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{
					Chat: types.JID{User: "120363144038483540", Server: types.NewsletterServer},
				},
				ID:        types.MessageID("post-1"),
				Timestamp: at,
			},
			Message: &waE2E.Message{Conversation: proto.String(text)},
		}
	}

	// Fresh post, unknown (unmuted) channel: notifies.
	c, n := newClient(t)
	c.notifyChannelPost(context.Background(), newEvent("hello followers", time.Now()))
	if n.count() != 1 {
		t.Fatalf("fresh post notified %d times, want 1", n.count())
	}

	// Muted channel: silent.
	c2, n2 := newClient(t)
	ctx := context.Background()
	if err := c2.store.SaveChannels(ctx, []appstore.Channel{{ID: "120363144038483540@newsletter", Name: "News", Muted: true}}); err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	c2.notifyChannelPost(ctx, newEvent("hello again", time.Now()))
	if n2.count() != 0 {
		t.Fatalf("muted channel notified %d times, want 0", n2.count())
	}

	// Stale post: silent.
	c3, n3 := newClient(t)
	c3.notifyChannelPost(context.Background(), newEvent("old news", time.Now().Add(-time.Hour)))
	if n3.count() != 0 {
		t.Fatalf("stale post notified %d times, want 0", n3.count())
	}
}
