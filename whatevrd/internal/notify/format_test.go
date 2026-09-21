package notify

import (
	"testing"

	"whatevrd/internal/app"
)

func TestParseCapabilities(t *testing.T) {
	caps := ParseCapabilities([]string{"actions", "body", "body-markup", "image-path", "persistence", "sound"})
	if !caps.Actions || !caps.Body || !caps.BodyMarkup || !caps.ImagePath || !caps.Persistence || !caps.Sound {
		t.Fatalf("capabilities not parsed: %+v", caps)
	}
}

func TestFormatDirectWithBody(t *testing.T) {
	content := FormatMessage(Capabilities{Body: true, Actions: true}, app.Message{Text: " hello\nthere "}, app.Chat{Name: "Alice"}, Options{Preview: true})
	if content.Summary != "Alice" || content.Body != "hello there" {
		t.Fatalf("unexpected content: %+v", content)
	}
	if len(content.Actions) == 0 {
		t.Fatal("expected action")
	}
}

func TestFormatGroupWithoutBody(t *testing.T) {
	content := FormatMessage(Capabilities{}, app.Message{SenderID: "12345@s.whatsapp.net", SenderName: "Alice", Text: "hello"}, app.Chat{Name: "Family", IsGroup: true}, Options{Preview: true})
	if content.Summary != "Family - Alice: hello" || content.Body != "" {
		t.Fatalf("unexpected content: %+v", content)
	}
}

func TestFormatGroupUsesWhatsAppSenderName(t *testing.T) {
	content := FormatMessage(Capabilities{Body: true}, app.Message{SenderID: "12345@s.whatsapp.net", SenderName: "~Alice", Text: "hello"}, app.Chat{Name: "Family", IsGroup: true}, Options{Preview: true})
	if content.Body != "~Alice: hello" {
		t.Fatalf("expected WhatsApp sender name in body, got %q", content.Body)
	}
}

func TestFormatGroupFallsBackToSenderID(t *testing.T) {
	content := FormatMessage(Capabilities{Body: true}, app.Message{SenderID: "12345@s.whatsapp.net", Text: "hello"}, app.Chat{Name: "Family", IsGroup: true}, Options{Preview: true})
	if content.Body != "12345: hello" {
		t.Fatalf("expected sender ID fallback in body, got %q", content.Body)
	}
}

// A media message has no text of its own, so its notification body is the
// one-line rendering the store computed for it. The notifier no longer has an
// opinion about what a photo is called.
func TestFormatMediaUsesStorePreview(t *testing.T) {
	content := FormatMessage(Capabilities{Body: true}, app.Message{MediaKind: "image", MediaMimeType: "image/jpeg", Preview: "📷 Photo"}, app.Chat{Name: "Alice"}, Options{Preview: true})
	if content.Body != "📷 Photo" {
		t.Fatalf("expected the store preview, got %q", content.Body)
	}
}

func TestFormatEscapesMarkup(t *testing.T) {
	content := FormatMessage(Capabilities{Body: true, BodyMarkup: true}, app.Message{Text: "<hello>"}, app.Chat{Name: "Alice"}, Options{Preview: true})
	if content.Body != "&lt;hello&gt;" {
		t.Fatalf("expected escaped markup, got %q", content.Body)
	}
}

func TestFormatPreviewDisabledHidesText(t *testing.T) {
	content := FormatMessage(Capabilities{Body: true}, app.Message{Text: "secret"}, app.Chat{Name: "Alice"}, Options{Preview: false})
	if content.Summary != "Alice" || content.Body != "New message" {
		t.Fatalf("expected hidden preview, got %+v", content)
	}
}

func TestFormatActionsIncludeReadAndReplyWhenSupported(t *testing.T) {
	content := FormatMessage(Capabilities{Actions: true, InlineReply: true}, app.Message{Text: "hello"}, app.Chat{Name: "Alice"}, Options{Preview: true})
	if len(content.Actions) != 6 || content.Actions[2] != "mark-read" || content.Actions[4] != "reply" {
		t.Fatalf("unexpected notification actions: %#v", content.Actions)
	}
	if content.Hints["x-kde-reply"] != "reply" {
		t.Fatalf("missing inline reply hint: %#v", content.Hints)
	}
}
