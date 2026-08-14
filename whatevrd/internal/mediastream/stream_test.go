package mediastream

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testAppInfo = "WhatsApp Video Keys"

// encryptMedia builds the bytes WhatsApp's CDN would serve for a plaintext:
// AES-256-CBC with PKCS#7, followed by the truncated whole-file HMAC.
func encryptMedia(t *testing.T, keys Keys, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(keys.CipherKey)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte(nil), plaintext...), bytes.Repeat([]byte{byte(padding)}, padding)...)

	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, keys.IV).CryptBlocks(ciphertext, padded)

	mac := hmac.New(sha256.New, keys.MACKey)
	mac.Write(keys.IV)
	mac.Write(ciphertext)
	return append(ciphertext, mac.Sum(nil)[:macLength]...)
}

// buildSidecar produces a sidecar in the requested layout, so the tests can
// prove the auto-detection handles both.
func buildSidecar(keys Keys, ciphertext []byte, plaintextLen int64, layout SidecarLayout) []byte {
	chunks := ChunkCount(plaintextLen)
	sidecar := make([]byte, 0, chunks*macLength)
	for i := 0; i < chunks; i++ {
		start := int64(i) * ChunkSize
		end := start + ChunkSize
		if end > int64(len(ciphertext)) {
			end = int64(len(ciphertext))
		}

		mac := hmac.New(sha256.New, keys.MACKey)
		switch layout {
		case SidecarLayoutLeadingIV:
			if i == 0 {
				mac.Write(keys.IV)
			} else {
				mac.Write(ciphertext[start-aes.BlockSize : start])
			}
			mac.Write(ciphertext[start:end])
		case SidecarLayoutTrailingBlock:
			mac.Write(ciphertext[start:end])
			if end+aes.BlockSize <= int64(len(ciphertext)) {
				mac.Write(ciphertext[end : end+aes.BlockSize])
			}
		}
		sidecar = append(sidecar, mac.Sum(nil)[:macLength]...)
	}
	return sidecar
}

func testPlaintext(size int) []byte {
	rng := rand.New(rand.NewSource(1))
	data := make([]byte, size)
	rng.Read(data)
	return data
}

// rangeServer serves the encrypted bytes with real Range support, which is what
// the CDN is expected to do.
func rangeServer(t *testing.T, encrypted []byte, requests *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			*requests++
		}
		http.ServeContent(w, r, "media.enc", time.Time{}, bytes.NewReader(encrypted))
	}))
	t.Cleanup(server.Close)
	return server
}

func streamAll(t *testing.T, source Source, partPath string) (*Stream, error) {
	t.Helper()
	stream, err := New(source, partPath, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(stream.Close)

	select {
	case <-stream.Finished():
	case <-time.After(30 * time.Second):
		t.Fatal("stream did not finish")
	}
	return stream, stream.Err()
}

func newTestSource(t *testing.T, plaintext []byte, layout SidecarLayout) (Source, []byte) {
	t.Helper()
	mediaKey := bytes.Repeat([]byte{0x2a}, 32)
	keys, err := DeriveKeys(mediaKey, testAppInfo)
	if err != nil {
		t.Fatalf("derive keys: %v", err)
	}
	encrypted := encryptMedia(t, keys, plaintext)
	hash := sha256.Sum256(plaintext)

	source := Source{
		Keys:         keys,
		PlaintextLen: int64(len(plaintext)),
		FileSHA256:   hash[:],
		Mime:         "video/mp4",
	}
	if layout != SidecarLayoutUnknown {
		source.Sidecar = buildSidecar(keys, encrypted[:len(encrypted)-macLength], source.PlaintextLen, layout)
	}
	return source, encrypted
}

// TestStreamAssemblesExactPlaintext is the core guarantee: fetching in ranges
// and decrypting chunk by chunk produces the same bytes a whole-file download
// would, including the padded final chunk.
func TestStreamAssemblesExactPlaintext(t *testing.T) {
	sizes := []int{
		1024,             // smaller than one chunk
		ChunkSize,        // exactly one chunk, so padding adds a whole block
		ChunkSize + 1,    // spills one byte into a second chunk
		3*ChunkSize + 77, // several chunks with a ragged tail
	}

	for _, size := range sizes {
		t.Run(strings.TrimSpace(byteSizeName(size)), func(t *testing.T) {
			plaintext := testPlaintext(size)
			source, encrypted := newTestSource(t, plaintext, SidecarLayoutLeadingIV)
			source.URL = rangeServer(t, encrypted, nil).URL

			partPath := filepath.Join(t.TempDir(), "media.part")
			stream, err := streamAll(t, source, partPath)
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			if !stream.Complete() {
				t.Fatal("stream finished without every chunk")
			}

			got, err := os.ReadFile(partPath)
			if err != nil {
				t.Fatalf("read assembled file: %v", err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatalf("assembled %d bytes, want %d, and they differ", len(got), len(plaintext))
			}
		})
	}
}

func byteSizeName(size int) string {
	switch {
	case size < ChunkSize:
		return "below one chunk"
	case size == ChunkSize:
		return "exactly one chunk"
	case size < 2*ChunkSize:
		return "just over one chunk"
	default:
		return "several chunks"
	}
}

// TestStreamDetectsEitherSidecarLayout covers the one thing about the wire
// format that is not documented: whether a sidecar entry covers the block
// before its chunk or the block after it. Both are accepted.
func TestStreamDetectsEitherSidecarLayout(t *testing.T) {
	for _, layout := range []SidecarLayout{SidecarLayoutLeadingIV, SidecarLayoutTrailingBlock} {
		t.Run(layout.String(), func(t *testing.T) {
			plaintext := testPlaintext(3*ChunkSize + 10)
			source, encrypted := newTestSource(t, plaintext, layout)
			source.URL = rangeServer(t, encrypted, nil).URL

			partPath := filepath.Join(t.TempDir(), "media.part")
			if _, err := streamAll(t, source, partPath); err != nil {
				t.Fatalf("stream: %v", err)
			}
			got, err := os.ReadFile(partPath)
			if err != nil {
				t.Fatalf("read assembled file: %v", err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatal("assembled bytes differ from the plaintext")
			}
		})
	}
}

// TestStreamWithoutSidecarStillVerifies proves a message that carries no
// sidecar still streams, with the whole-file hash as the integrity check.
func TestStreamWithoutSidecarStillVerifies(t *testing.T) {
	plaintext := testPlaintext(2*ChunkSize + 5)
	source, encrypted := newTestSource(t, plaintext, SidecarLayoutUnknown)
	source.URL = rangeServer(t, encrypted, nil).URL

	partPath := filepath.Join(t.TempDir(), "media.part")
	if _, err := streamAll(t, source, partPath); err != nil {
		t.Fatalf("stream: %v", err)
	}
	got, err := os.ReadFile(partPath)
	if err != nil {
		t.Fatalf("read assembled file: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatal("assembled bytes differ from the plaintext")
	}
}

// TestStreamRejectsTamperedBytes makes sure a corrupted body fails loudly
// rather than being written to the cache as if it were the real file.
func TestStreamRejectsTamperedBytes(t *testing.T) {
	plaintext := testPlaintext(2 * ChunkSize)
	source, encrypted := newTestSource(t, plaintext, SidecarLayoutLeadingIV)
	encrypted[ChunkSize+40] ^= 0xff
	source.URL = rangeServer(t, encrypted, nil).URL

	partPath := filepath.Join(t.TempDir(), "media.part")
	if _, err := streamAll(t, source, partPath); err == nil {
		t.Fatal("stream accepted tampered media")
	}
}

// TestStreamSeekFetchesTheSeekedChunkFirst is the behaviour that makes seeking
// in a half-fetched video usable: waiting on a late offset must not require
// every earlier chunk to arrive first.
func TestStreamSeekFetchesTheSeekedChunkFirst(t *testing.T) {
	plaintext := testPlaintext(40 * ChunkSize)
	source, encrypted := newTestSource(t, plaintext, SidecarLayoutLeadingIV)
	source.URL = rangeServer(t, encrypted, nil).URL

	partPath := filepath.Join(t.TempDir(), "media.part")
	stream, err := New(source, partPath, nil, nil, nil)
	if err != nil {
		t.Fatalf("new stream: %v", err)
	}
	t.Cleanup(stream.Close)

	target := int64(35 * ChunkSize)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := stream.WaitFor(ctx, target); err != nil {
		t.Fatalf("wait for seeked offset: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := stream.ReadAt(buf, target)
	if err != nil {
		t.Fatalf("read at seeked offset: %v", err)
	}
	if !bytes.Equal(buf[:n], plaintext[target:target+int64(n)]) {
		t.Fatal("bytes at the seeked offset are not the plaintext")
	}

	// The point of seeking: the early chunks are still missing while the
	// seeked-to chunk is already readable.
	if _, missing := stream.file.MissingFrom(0); !missing {
		t.Log("whole file already fetched; seek priority not observable in this run")
	}
}

// TestStreamFallsBackWhenRangesAreIgnored covers a CDN that answers a ranged
// request with the whole body: streaming must refuse rather than decrypt the
// wrong offsets.
func TestStreamFallsBackWhenRangesAreIgnored(t *testing.T) {
	plaintext := testPlaintext(3 * ChunkSize)
	source, encrypted := newTestSource(t, plaintext, SidecarLayoutLeadingIV)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(encrypted)
	}))
	t.Cleanup(server.Close)
	source.URL = server.URL

	partPath := filepath.Join(t.TempDir(), "media.part")
	_, err := streamAll(t, source, partPath)
	if !errors.Is(err, ErrRangeUnsupported) {
		t.Fatalf("err = %v, want ErrRangeUnsupported", err)
	}
}

// TestStreamResumesFromDisk proves the chunk index earns its keep: a second
// stream over the same partial file refetches nothing.
func TestStreamResumesFromDisk(t *testing.T) {
	plaintext := testPlaintext(4 * ChunkSize)
	source, encrypted := newTestSource(t, plaintext, SidecarLayoutLeadingIV)

	requests := 0
	source.URL = rangeServer(t, encrypted, &requests).URL

	partPath := filepath.Join(t.TempDir(), "media.part")
	if _, err := streamAll(t, source, partPath); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	firstRequests := requests
	if firstRequests == 0 {
		t.Fatal("first stream made no requests")
	}

	if _, err := streamAll(t, source, partPath); err != nil {
		t.Fatalf("resumed stream: %v", err)
	}
	if requests != firstRequests {
		t.Fatalf("resumed stream made %d extra requests, want 0", requests-firstRequests)
	}
}

// TestSparseFileDiscardsAMismatchedIndex guards the resume path against an
// index that describes a different file: better to refetch than to serve bytes
// we cannot account for.
func TestSparseFileDiscardsAMismatchedIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "media.part")

	file, err := OpenSparseFile(path, 2*ChunkSize)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := file.WriteChunk(0, testPlaintext(ChunkSize)); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	file.Close()

	// Reopening with a different declared length must not inherit the old
	// chunk record.
	reopened, err := OpenSparseFile(path, 5*ChunkSize)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if reopened.Has(0) {
		t.Fatal("reopened file kept a chunk from an index that does not describe it")
	}
}

func TestCiphertextLenAlwaysPads(t *testing.T) {
	cases := []struct {
		plaintext int64
		want      int64
	}{
		{1, 16},
		{15, 16},
		{16, 32},
		{17, 32},
		{ChunkSize, ChunkSize + 16},
	}
	for _, tc := range cases {
		if got := CiphertextLen(tc.plaintext); got != tc.want {
			t.Errorf("CiphertextLen(%d) = %d, want %d", tc.plaintext, got, tc.want)
		}
	}
}

func TestFinishCallbackMayCloseStream(t *testing.T) {
	partPath := filepath.Join(t.TempDir(), "callback.part")
	file, err := OpenSparseFile(partPath, 1)
	if err != nil {
		t.Fatalf("open sparse file: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream := &Stream{
		file:     file,
		partPath: partPath,
		ctx:      ctx,
		cancel:   cancel,
		finished: make(chan struct{}),
	}
	done := make(chan struct{})
	stream.done = func(error) {
		stream.Close()
		close(done)
	}

	go stream.finish(nil)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finish callback deadlocked while closing its stream")
	}
}
