package wa

import (
	"os"
	"path/filepath"
	"testing"
)

// webpHeader builds a minimal RIFF/WebP header with the given top-level chunk
// fourCC and (for VP8X) flags byte, padded past the 21-byte detection window.
func webpHeader(fourCC string, vp8xFlags byte) []byte {
	h := make([]byte, 32)
	copy(h[0:4], "RIFF")
	copy(h[8:12], "WEBP")
	copy(h[12:16], fourCC)
	if fourCC == "VP8X" {
		h[20] = vp8xFlags
	}
	return h
}

func TestIsAnimatedWebP(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"animated VP8X", webpHeader("VP8X", 0x02), true},
		{"animated VP8X with other flags", webpHeader("VP8X", 0x12), true},
		{"static VP8X", webpHeader("VP8X", 0x10), false},
		{"simple lossy VP8 ", webpHeader("VP8 ", 0), false},
		{"lossless VP8L", webpHeader("VP8L", 0), false},
		{"not a webp", []byte("\x89PNG\r\n\x1a\n0000000000000"), false},
		{"too short", []byte("RIFF\x00\x00\x00\x00WEBPVP8X"), false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAnimatedWebP(tc.data); got != tc.want {
				t.Fatalf("isAnimatedWebP(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestFileLooksAnimatedWebP(t *testing.T) {
	dir := t.TempDir()

	animated := filepath.Join(dir, "anim.webp")
	if err := os.WriteFile(animated, webpHeader("VP8X", 0x02), 0o600); err != nil {
		t.Fatalf("write animated: %v", err)
	}
	if !fileLooksAnimatedWebP(animated) {
		t.Error("animated WebP file not detected as animated")
	}

	static := filepath.Join(dir, "static.webp")
	if err := os.WriteFile(static, webpHeader("VP8 ", 0), 0o600); err != nil {
		t.Fatalf("write static: %v", err)
	}
	if fileLooksAnimatedWebP(static) {
		t.Error("static WebP file detected as animated")
	}

	if fileLooksAnimatedWebP(filepath.Join(dir, "missing.webp")) {
		t.Error("missing file reported as animated")
	}
}
