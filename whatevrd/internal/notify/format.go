package notify

import (
	"html"
	"strings"
	"unicode/utf8"

	"whatevrd/internal/app"
	"whatevrd/internal/textutil"
)

const previewLimit = 200

type Capabilities struct {
	Actions     bool
	Body        bool
	BodyMarkup  bool
	IconStatic  bool
	ImagePath   bool
	Persistence bool
	Sound       bool
	InlineReply bool
}

type Content struct {
	Summary string
	Body    string
	Icon    string
	Actions []string
	Hints   map[string]any
}

// Options carries the user's notification preferences into formatting. Preview
// off hides the message text (sender/chat only); Sound asks the server to play
// its default message sound when the daemon supports it.
type Options struct {
	Preview bool
	Sound   bool
}

func ParseCapabilities(values []string) Capabilities {
	var caps Capabilities
	for _, value := range values {
		switch value {
		case "actions":
			caps.Actions = true
		case "body":
			caps.Body = true
		case "body-markup":
			caps.BodyMarkup = true
		case "icon-static":
			caps.IconStatic = true
		case "image-path":
			caps.ImagePath = true
		case "persistence":
			caps.Persistence = true
		case "sound":
			caps.Sound = true
		case "inline-reply", "x-kde-reply":
			caps.InlineReply = true
		}
	}
	return caps
}

func FormatMessage(caps Capabilities, message app.Message, chat app.Chat, opts Options) Content {
	chatName := strings.TrimSpace(chat.Name)
	if chatName == "" {
		chatName = chat.ID
	}

	var preview string
	if opts.Preview {
		preview = previewText(message)
		if chat.IsGroup && message.SenderID != "" && message.SenderID != "me" {
			preview = senderDisplay(message) + ": " + preview
		}
	} else {
		// Preview disabled: never leak message content.
		preview = "New message"
	}

	content := Content{
		Summary: chatName,
		Icon:    "in.codelif.Whatevr",
		Hints: map[string]any{
			"desktop-entry": "in.codelif.Whatevr",
			"category":      "im.received",
		},
	}

	// Sound is played by the worker itself (see playSound); we deliberately do
	// not set the "sound-name" hint, so servers that honour it don't double up
	// with our own playback. caps.Sound is still parsed for capability probing.

	if caps.Actions {
		content.Actions = []string{"default", "Open Chat", "mark-read", "Mark as read"}
		if caps.InlineReply {
			content.Actions = append(content.Actions, "reply", "Reply")
			content.Hints["x-kde-reply"] = "reply"
		}
	}

	if (caps.ImagePath || caps.IconStatic) && strings.TrimSpace(chat.AvatarLocalPath) != "" {
		content.Icon = chat.AvatarLocalPath
		if caps.ImagePath {
			content.Hints["image-path"] = chat.AvatarLocalPath
		}
	}

	if caps.Body {
		content.Body = preview
		if caps.BodyMarkup {
			content.Body = html.EscapeString(preview)
		}
		return content
	}

	if chat.IsGroup {
		content.Summary = chatName + " - " + preview
	} else {
		content.Summary = chatName + ": " + preview
	}
	content.Summary = truncate(content.Summary, previewLimit)
	return content
}

// previewText is the notification body. It rides the one-line rendering the
// store already computed, so a voice note reads "🎤 Voice message (0:12)" here
// exactly as it does in the chat list, rather than falling through to the
// mime-sniffing this used to do (which had never heard of voice notes or
// documents and announced both as "New message").
func previewText(message app.Message) string {
	line := strings.TrimSpace(message.Preview)
	if line == "" {
		// Every message the daemon publishes carries a Preview. Falling back to
		// the raw text costs nothing and keeps a caller that built a Message by
		// hand from announcing "New message" for something with words in it.
		line = strings.TrimSpace(message.Text)
	}
	text := textutil.ExpandMentions(line, toTextutilMentions(message.Mentions))
	if text == "" {
		text = "New message"
	}
	text = strings.Join(strings.Fields(text), " ")
	return truncate(text, previewLimit)
}

func toTextutilMentions(mentions []app.Mention) []textutil.Mention {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]textutil.Mention, len(mentions))
	for i, m := range mentions {
		out[i] = textutil.Mention{JID: m.JID, DisplayName: m.DisplayName}
	}
	return out
}

func senderDisplay(message app.Message) string {
	if name := strings.TrimSpace(message.SenderName); name != "" {
		return name
	}
	senderID := message.SenderID
	if at := strings.IndexByte(senderID, '@'); at > 0 {
		return senderID[:at]
	}
	return senderID
}

func truncate(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
