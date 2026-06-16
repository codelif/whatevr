package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDaemonConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Missing key returns empty, no error.
	if value, err := db.GetDaemonConfig(ctx, "app_preferences"); err != nil || value != "" {
		t.Fatalf("expected empty value for missing key, got %q err %v", value, err)
	}

	if err := db.SetDaemonConfig(ctx, "app_preferences", `{"notificationsEnabled":true}`); err != nil {
		t.Fatalf("set: %v", err)
	}
	if value, err := db.GetDaemonConfig(ctx, "app_preferences"); err != nil || value != `{"notificationsEnabled":true}` {
		t.Fatalf("unexpected value %q err %v", value, err)
	}

	// Upsert overwrites.
	if err := db.SetDaemonConfig(ctx, "app_preferences", `{"notificationsEnabled":false}`); err != nil {
		t.Fatalf("update: %v", err)
	}
	if value, _ := db.GetDaemonConfig(ctx, "app_preferences"); value != `{"notificationsEnabled":false}` {
		t.Fatalf("expected overwritten value, got %q", value)
	}
}
