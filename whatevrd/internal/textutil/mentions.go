// Package textutil holds small text-formatting helpers shared across the daemon.
package textutil

import "strings"

// Mention pairs a mentioned contact's JID with the display name to render.
type Mention struct {
	JID         string
	DisplayName string
}

// ExpandMentions rewrites "@<userpart>" tokens in text to "@<DisplayName>",
// keyed by the user-part (the substring before '@') of each mention's JID. It
// mirrors the frontend's mention rendering (whatkevr messagemarkup) for use in
// plain-text previews and notifications. It is a no-op when there are no
// mentions with a name or the text contains no '@'.
func ExpandMentions(text string, mentions []Mention) string {
	if len(mentions) == 0 || !strings.Contains(text, "@") {
		return text
	}

	byUserpart := make(map[string]string, len(mentions))
	for _, m := range mentions {
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			continue
		}
		userpart := m.JID
		if at := strings.IndexByte(userpart, '@'); at > 0 {
			userpart = userpart[:at]
		}
		if userpart == "" {
			continue
		}
		byUserpart[userpart] = name
	}
	if len(byUserpart) == 0 {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		if text[i] != '@' {
			b.WriteByte(text[i])
			i++
			continue
		}
		// A mention token is '@' followed by the run of digits that make up the
		// contact's phone user-part.
		j := i + 1
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}
		if name, ok := byUserpart[text[i+1:j]]; ok {
			b.WriteByte('@')
			b.WriteString(name)
		} else {
			b.WriteString(text[i:j])
		}
		i = j
	}
	return b.String()
}
