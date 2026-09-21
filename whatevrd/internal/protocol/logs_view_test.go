package protocol

import "testing"

func TestLogsAssignStableIDs(t *testing.T) {
	s := &logsSession{seqs: make(map[string]uint64)}

	first := s.assign([]string{"a", "b", "a"})
	if len(first) != 3 {
		t.Fatalf("got %d items, want 3", len(first))
	}
	ids := map[string]bool{}
	for _, it := range first {
		ids[it.ID] = true
	}
	if len(ids) != 3 {
		t.Fatalf("duplicate lines must keep distinct ids: %v", ids)
	}
	// Same window again: identical ids and sorts, no churn.
	second := s.assign([]string{"a", "b", "a"})
	for i := range second {
		if second[i].ID != first[i].ID || second[i].Sort != first[i].Sort {
			t.Fatalf("refresh churned row %d: %s/%s -> %s/%s", i,
				first[i].ID, first[i].Sort, second[i].ID, second[i].Sort)
		}
	}
	// Rule 3: the id must also ride inside Data — CollectionViewModel keys
	// rows by item["id"], so a keyless row is dropped and the Logs tab stays
	// empty.
	for _, it := range first {
		data, ok := it.Data.(logsItem)
		if !ok || data.ID != it.ID {
			t.Fatalf("row data id = %+v; want %q inside", it.Data, it.ID)
		}
	}
	// Ring slides: the oldest line leaves, a new one appends. Survivors keep
	// their identity — "b" (the second row) and "a" (now the first copy, so
	// it reclaims the raw-line key) — and the new line appends after them.
	third := s.assign([]string{"b", "a", "c"})
	if third[0].ID != first[1].ID || third[1].ID != first[0].ID {
		t.Fatalf("survivors must keep ids: %v vs %v", third, first)
	}
	if third[2].ID == first[0].ID || third[2].ID == first[1].ID || third[2].ID == first[2].ID {
		t.Fatalf("new line reused a retired id: %s", third[2].ID)
	}
}

func TestParseLogLine(t *testing.T) {
	item := parseLogLine("2026/09/15 23:14:00 store: search failed: ERROR somewhere")
	if item.Time != "2026/09/15 23:14:00" || item.Level != "error" {
		t.Fatalf("stamp/level: %+v", item)
	}
	if item.Text != "store: search failed: ERROR somewhere" {
		t.Fatalf("text: %q", item.Text)
	}
	plain := parseLogLine("tray: activated (no window in daemon mode)")
	if plain.Time != "" || plain.Level != "info" || plain.Text != "tray: activated (no window in daemon mode)" {
		t.Fatalf("no-stamp line: %+v", plain)
	}
}
