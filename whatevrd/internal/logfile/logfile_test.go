package logfile

import (
	"os"
	"testing"
)

// TestLogFileTailsAndRotates locks in that recent lines are served oldest
// first and that the file rolls over at the size cap instead of growing
// without bound.
func TestLogFileTailsAndRotates(t *testing.T) {
	h := Init(t.TempDir())
	defer h.Close()
	if h.Path() == "" {
		t.Fatal("log file path is empty")
	}

	for _, line := range []string{"first", "second", "third"} {
		if _, err := h.Write([]byte(line + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	tail := h.Tail(2)
	if len(tail) != 2 || tail[0] != "second" || tail[1] != "third" {
		t.Fatalf("tail = %q, want [second third]", tail)
	}

	// Force rotation: the lines above plus a cap-sized chunk overflow the
	// file, rolling it to .1. A single write is never split, so the chunk
	// itself must fit the cap.
	big := make([]byte, maxBytes)
	for i := range big {
		big[i] = 'x'
	}
	if _, err := h.Write(big); err != nil {
		t.Fatalf("write big chunk: %v", err)
	}
	if _, err := os.Stat(h.Path() + ".1"); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	info, err := os.Stat(h.Path())
	if err != nil {
		t.Fatalf("active log missing: %v", err)
	}
	if info.Size() > maxBytes {
		t.Fatalf("active log size = %d, exceeds cap %d", info.Size(), maxBytes)
	}
}
