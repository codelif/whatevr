package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestClearSessionDataWipesEverythingExceptKeepList seeds a row into every
// table in the schema and asserts ClearSessionData empties all of them except
// the explicit keep-list. Because the wipe enumerates sqlite_master, a table
// added in the future is wiped by default; this test then only fails if
// someone adds a table to the keep-list without updating the expectation here.
func TestClearSessionDataWipesEverythingExceptKeepList(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := db.conn.ExecContext(ctx, `INSERT INTO daemon_config (key, value) VALUES ('pref', 'kept')`); err != nil {
		t.Fatalf("seed daemon_config: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `INSERT INTO app_state (key, value) VALUES ('sync', 'wiped')`); err != nil {
		t.Fatalf("seed app_state: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `INSERT INTO chats (id, name) VALUES ('chat@s.whatsapp.net', 'Chat')`); err != nil {
		t.Fatalf("seed chats: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, timestamp, direction, status)
		VALUES ('msg1', 'chat@s.whatsapp.net', 1, 'incoming', 'delivered')
	`); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `
		INSERT INTO avatars (subject_kind, subject_id) VALUES ('chat', 'chat@s.whatsapp.net')
	`); err != nil {
		t.Fatalf("seed avatars: %v", err)
	}

	tables, virtual, err := db.listTables(ctx)
	if err != nil {
		t.Fatalf("listTables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("listTables returned no tables")
	}
	tables = append(tables, virtual...)

	for keep := range clearSessionKeepTables {
		if keep != "daemon_config" {
			t.Errorf("unexpected table on the logout keep-list: %s (account data must not survive logout)", keep)
		}
	}

	if err := db.ClearSessionData(ctx); err != nil {
		t.Fatalf("ClearSessionData: %v", err)
	}

	for _, table := range tables {
		var count int
		if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+table+`"`).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if clearSessionKeepTables[table] {
			if table == "daemon_config" && count == 0 {
				t.Errorf("daemon_config was wiped; app preferences must survive logout")
			}
			continue
		}
		if count != 0 {
			t.Errorf("table %s still has %d rows after ClearSessionData", table, count)
		}
	}
}
