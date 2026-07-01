package textutil

import "testing"

func TestExpandMentions(t *testing.T) {
	mentions := []Mention{
		{JID: "919876543210@s.whatsapp.net", DisplayName: "Alice"},
		{JID: "14155550001@s.whatsapp.net", DisplayName: "Bob"},
		{JID: "14155550002@s.whatsapp.net", DisplayName: ""}, // unnamed: left as-is
	}

	cases := []struct {
		name     string
		text     string
		mentions []Mention
		want     string
	}{
		{"no mentions", "hello @919876543210", nil, "hello @919876543210"},
		{"no at sign", "just text", mentions, "just text"},
		{"single", "hi @919876543210!", mentions, "hi @Alice!"},
		{"multiple", "@919876543210 and @14155550001", mentions, "@Alice and @Bob"},
		{"unknown number", "hey @10000000000", mentions, "hey @10000000000"},
		{"unnamed mention kept raw", "yo @14155550002", mentions, "yo @14155550002"},
		{"bare at kept", "email a@b test @919876543210", mentions, "email a@b test @Alice"},
		{"repeated", "@919876543210 @919876543210", mentions, "@Alice @Alice"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandMentions(tc.text, tc.mentions); got != tc.want {
				t.Fatalf("ExpandMentions(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
