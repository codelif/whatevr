package wa

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	appstore "whatevrd/internal/store"
)

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode test jpeg: %v", err)
	}
	return buf.Bytes()
}

// TestDownscaleImageForStandard locks in the HD contract: small photos pass
// through byte-identical, large ones come back at most 1600px on a side as
// JPEG with real dimensions reported.
func TestDownscaleImageForStandard(t *testing.T) {
	small := testJPEG(t, 800, 600)
	out, mime, ext, w, h := downscaleImageForStandard(small, "image/jpeg")
	if !bytes.Equal(out, small) || mime != "image/jpeg" || ext != ".jpg" || w != 800 || h != 600 {
		t.Fatalf("small image changed: mime=%s ext=%s %dx%d", mime, ext, w, h)
	}

	large := testJPEG(t, 3200, 2400)
	out, mime, ext, w, h = downscaleImageForStandard(large, "image/jpeg")
	if mime != "image/jpeg" || ext != ".jpg" {
		t.Fatalf("scaled mime/ext = %s/%s, want image/jpeg/.jpg", mime, ext)
	}
	if w != 1600 || h != 1200 {
		t.Fatalf("scaled dims = %dx%d, want 1600x1200", w, h)
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(out)); err != nil || cfg.Width != 1600 || cfg.Height != 1200 {
		t.Fatalf("scaled bytes decode = %+v, %v; want 1600x1200", cfg, err)
	}
}

// TestDocumentSendKeepsFilename is a regression test: documents sent through
// the outbound path must carry their real basename on the wire, never the
// "file" fallback (receivers showed just "file" for batch-sent documents).
func TestDocumentSendKeepsFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hallticket-2026.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4 fake body for name test"), 0o600); err != nil {
		t.Fatalf("write temp pdf: %v", err)
	}

	_, _, _, mediaKind, fileName, err := readOutboundMedia(path, MediaSendOptions{Kind: "document"})
	if err != nil {
		t.Fatalf("readOutboundMedia: %v", err)
	}
	if mediaKind != appstore.MediaKindDocument {
		t.Fatalf("kind = %q, want document", mediaKind)
	}
	if fileName != "hallticket-2026.pdf" {
		t.Fatalf("outbound fileName = %q, want the source basename", fileName)
	}

	envelope, _, _, err := buildOutgoingMediaMessage(appstore.Message{
		MediaKind:     appstore.MediaKindDocument,
		MediaMimeType: "application/pdf",
		MediaFileName: fileName,
	}, []byte("fake body"), "application/pdf")
	if err != nil {
		t.Fatalf("buildOutgoingMediaMessage: %v", err)
	}
	doc := envelope.GetDocumentMessage()
	if doc == nil {
		t.Fatalf("envelope is not a document message: %+v", envelope)
	}
	if doc.GetFileName() != "hallticket-2026.pdf" {
		t.Fatalf("wire FileName = %q, want the source basename", doc.GetFileName())
	}
}

// TestVoiceSendNormalizesOggMime locks in the cross-device voice contract:
// desktop recordings sniff as "application/ogg" under Go's sniffer, but no
// phone client sends PTT with that mimetype — such notes never appeared on
// mobile. The outbound path rewrites Ogg voice to "audio/ogg; codecs=opus".
func TestVoiceSendNormalizesOggMime(t *testing.T) {
	cases := map[string]string{
		"application/ogg":   "audio/ogg; codecs=opus",
		"application/x-ogg": "audio/ogg; codecs=opus",
		"audio/ogg":         "audio/ogg; codecs=opus",
		"audio/opus":        "audio/ogg; codecs=opus",
	}
	for in, want := range cases {
		if got := outboundVoiceMime(in, "whatevr-voice-1.ogg"); got != want {
			t.Fatalf("outboundVoiceMime(%q) = %q, want %q", in, got, want)
		}
	}
	// Non-Ogg audio passes through untouched.
	if got := outboundVoiceMime("audio/mpeg", "note.mp3"); got != "audio/mpeg" {
		t.Fatalf("outboundVoiceMime(audio/mpeg) = %q, want passthrough", got)
	}
}

// TestDocumentSendSkipsMediaSizeCap locks in that "send as document" is not
// size-checked like media: a 30 MiB video file staged as a document passes
// the 25 MiB media ceiling, while the same file as video is still rejected.
func TestDocumentSendSkipsMediaSizeCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if err := f.Truncate(30 * 1024 * 1024); err != nil {
		f.Close()
		t.Fatalf("sparsify temp file: %v", err)
	}
	f.Close()

	if _, _, _, _, _, err := readOutboundMedia(path, MediaSendOptions{Kind: "document"}); err != nil {
		t.Fatalf("document readOutboundMedia: %v", err)
	}
	if _, _, _, _, _, err := readOutboundMedia(path, MediaSendOptions{Kind: "video"}); err == nil {
		t.Fatal("expected video readOutboundMedia to reject a 30 MiB file")
	}
}
