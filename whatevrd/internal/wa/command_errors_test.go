package wa

import (
	"context"
	"testing"

	"whatevrd/internal/app"
)

// The raw-JID commands must reject malformed input with a structured
// CommandError so frontend boundaries classify them as invalid params instead
// of internal (the protocol layer's substring fallback must not be load-bearing).
func TestRawJIDCommandsRaiseInvalidArgument(t *testing.T) {
	c := &Client{}
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
	}{
		{"EnsureDirectChat malformed", func() error { _, err := c.EnsureDirectChat(ctx, "not a jid"); return err }},
		{"EnsureDirectChat group", func() error { _, err := c.EnsureDirectChat(ctx, "1234-5678@g.us"); return err }},
		{"GetContactInfo malformed", func() error { _, err := c.GetContactInfo(ctx, "not a jid"); return err }},
		{"GetContactInfo group", func() error { _, err := c.GetContactInfo(ctx, "1234-5678@g.us"); return err }},
		{"FetchProfilePicture malformed", func() error { _, err := c.FetchProfilePicture(ctx, "@@"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("expected an error")
			}
			ce, ok := app.AsCommandError(err)
			if !ok {
				t.Fatalf("expected a CommandError, got %T: %v", err, err)
			}
			if ce.Kind != app.CommandErrorInvalidArgument {
				t.Fatalf("expected CommandErrorInvalidArgument, got kind %d (%s)", ce.Kind, ce.Message)
			}
		})
	}
}

// errNotConnected must carry the structured not-connected kind so every use
// site inherits correct classification without message matching.
func TestErrNotConnectedIsStructured(t *testing.T) {
	ce, ok := app.AsCommandError(errNotConnected)
	if !ok {
		t.Fatalf("errNotConnected is not a CommandError: %T", errNotConnected)
	}
	if ce.Kind != app.CommandErrorNotConnected {
		t.Fatalf("expected CommandErrorNotConnected, got kind %d", ce.Kind)
	}
}
