package wa

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildWebP assembles a minimal but structurally valid WebP container. Chunks
// are synthesised rather than shipped as a fixture so each case states exactly
// which container shape it is about; nothing here decodes image data.
func buildWebP(vp8xFlags byte, chunks []byte) []byte {
	// VP8X payload is exactly 10 bytes: flags, 3 reserved, canvas width-1 and
	// height-1 as 24-bit little-endian.
	vp8xPayload := []byte{vp8xFlags, 0, 0, 0}
	vp8xPayload = append(vp8xPayload, encode24(511)...)
	vp8xPayload = append(vp8xPayload, encode24(511)...)

	body := append([]byte("WEBP"), chunk("VP8X", vp8xPayload)...)
	body = append(body, chunks...)

	out := []byte("RIFF")
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

func chunk(id string, payload []byte) []byte {
	out := []byte(id)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(payload)))
	out = append(out, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func encode24(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
}

// animationFrame builds one ANMF chunk: the 16-byte frame header followed by
// the frame's own image chunks, with an ALPH plane when the frame carries one.
func animationFrame(withAlpha bool, imageBytes int) []byte {
	payload := make([]byte, webpANMFHeaderSize)
	if withAlpha {
		payload = append(payload, chunk("ALPH", make([]byte, 8))...)
	}
	return chunk("ANMF", append(payload, chunk("VP8 ", make([]byte, imageBytes))...))
}

func animationChunks(frameAlpha ...bool) []byte {
	out := chunk("ANIM", make([]byte, 6))
	for i, withAlpha := range frameAlpha {
		// Odd sizes on purpose, so a mistake in the RIFF padding rule
		// desynchronises the walk and the test notices.
		out = append(out, animationFrame(withAlpha, 7+i)...)
	}
	return out
}

func TestRepairWebPAlphaFlagBytes(t *testing.T) {
	// An animation whose frames carry ALPH deltas while VP8X claims the canvas
	// is opaque: Qt skips compositing and renders the alpha mask as holes, so
	// the flag has to be set. The first frame is the opaque keyframe, exactly
	// as WhatsApp's video-to-sticker converter emits it.
	interFrame := buildWebP(webpAnimationFlag, animationChunks(false, true, true))
	if !repairWebPAlphaFlagBytes(interFrame) {
		t.Fatal("expected the missing VP8X alpha flag to be repaired")
	}
	if interFrame[webpVP8XFlagOffset]&webpAlphaFlag == 0 {
		t.Fatal("alpha flag was reported repaired but is still clear")
	}
	if repairWebPAlphaFlagBytes(interFrame) {
		t.Fatal("repair is not idempotent")
	}

	cases := []struct {
		name string
		data []byte
	}{
		{"animation with no frame alpha", buildWebP(webpAnimationFlag, animationChunks(false, false))},
		{"animation that already declares alpha", buildWebP(webpAnimationFlag|webpAlphaFlag, animationChunks(false, true))},
		{"still image with alpha", buildWebP(webpAlphaFlag, chunk("ALPH", make([]byte, 8)))},
		{"simple webp with no VP8X", append([]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), make([]byte, 8)...)},
		{"not a webp", []byte("GIF89a and then some bytes")},
		{"truncated header", []byte("RIFF\x04\x00")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]byte(nil), tc.data...)
			if repairWebPAlphaFlagBytes(tc.data) {
				t.Fatal("expected no repair")
			}
			if string(before) != string(tc.data) {
				t.Fatal("bytes were modified despite reporting no repair")
			}
		})
	}
}

// A frame whose payload is too short to hold a sub-chunk header must not send
// the walk reading past the chunk, and a chunk claiming more bytes than the
// file holds must stop it rather than error out.
func TestRepairWebPAlphaFlagBytesToleratesMalformedFrames(t *testing.T) {
	short := buildWebP(webpAnimationFlag, append(chunk("ANIM", make([]byte, 6)),
		chunk("ANMF", make([]byte, webpANMFHeaderSize))...))
	if repairWebPAlphaFlagBytes(short) {
		t.Fatal("expected no repair for a frame with no image chunk")
	}

	overlong := buildWebP(webpAnimationFlag, animationChunks(false, true))
	// Rewrite the ANIM chunk size to run past the end of the buffer.
	animOffset := webpRIFFHeaderSize + 8 + 10
	binary.LittleEndian.PutUint32(overlong[animOffset+4:animOffset+8], 1<<20)
	if repairWebPAlphaFlagBytes(overlong) {
		t.Fatal("expected no repair once the chunk table is unwalkable")
	}
}

func TestRepairWebPAlphaFlagFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sticker.webp")
	data := buildWebP(webpAnimationFlag, animationChunks(false, true, true))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := repairWebPAlphaFlagFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected the file to be repaired")
	}

	repaired, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if repaired[webpVP8XFlagOffset]&webpAlphaFlag == 0 {
		t.Fatal("alpha flag is still clear on disk")
	}
	// The repair is a single byte at a fixed offset: nothing else may move.
	data[webpVP8XFlagOffset] |= webpAlphaFlag
	if string(repaired) != string(data) {
		t.Fatal("repair changed more than the VP8X flags byte")
	}

	changed, err = repairWebPAlphaFlagFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second pass repaired an already-correct file")
	}
}
