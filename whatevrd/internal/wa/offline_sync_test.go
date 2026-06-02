package wa

import "testing"

func TestOfflineSyncPercent(t *testing.T) {
	cases := []struct {
		name      string
		processed uint32
		total     uint32
		complete  bool
		want      uint32
	}{
		{name: "unknown total", processed: 10, total: 0, want: 0},
		{name: "partial", processed: 25, total: 100, want: 25},
		{name: "caps incomplete", processed: 100, total: 100, want: 99},
		{name: "complete", processed: 100, total: 100, complete: true, want: 100},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := offlineSyncPercent(tc.processed, tc.total, tc.complete); got != tc.want {
				t.Fatalf("offlineSyncPercent() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNonNegativeUint32(t *testing.T) {
	if got := nonNegativeUint32(-1); got != 0 {
		t.Fatalf("nonNegativeUint32(-1) = %d, want 0", got)
	}
	if got := nonNegativeUint32(42); got != 42 {
		t.Fatalf("nonNegativeUint32(42) = %d, want 42", got)
	}
}
