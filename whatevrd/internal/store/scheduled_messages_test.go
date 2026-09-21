package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestScheduledMessagesAreDurableAndDue(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sendAt := time.Unix(100, 0)
	id, err := db.ScheduleText(ctx, "123@s.whatsapp.net", "hello", sendAt)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.DueScheduledMessages(ctx, time.Unix(101, 0), 10)
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].Text != "hello" {
		t.Fatalf("due rows: id=%d rows=%+v err=%v", id, rows, err)
	}
	if err := db.DeleteScheduledMessage(ctx, id); err != nil {
		t.Fatal(err)
	}
	rows, err = db.DueScheduledMessages(ctx, time.Unix(101, 0), 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleted rows: %+v err=%v", rows, err)
	}
}
