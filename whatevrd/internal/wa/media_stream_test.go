package wa

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	"whatevrd/internal/app"
	"whatevrd/internal/mediastream"
	appstore "whatevrd/internal/store"
)

func newMediaServerClient(t *testing.T) *Client {
	t.Helper()
	client := &Client{
		daemon: app.NewDaemon(app.Paths{}),
		log:    waLog.Noop,
		paths:  app.Paths{MediaCacheDir: t.TempDir()},
	}
	if err := client.StartMediaServer(); err != nil {
		t.Fatalf("start media server: %v", err)
	}
	t.Cleanup(client.StopMediaServer)
	return client
}

// registerFakeStream puts a completed stream in the map so the server has
// something to serve without going near WhatsApp.
func registerFakeStream(t *testing.T, client *Client, messageID string, plaintext []byte) {
	t.Helper()

	partPath := filepath.Join(t.TempDir(), messageID+".part")
	file, err := mediastream.OpenSparseFile(partPath, int64(len(plaintext)))
	if err != nil {
		t.Fatalf("open sparse file: %v", err)
	}
	for i := 0; i < mediastream.ChunkCount(int64(len(plaintext))); i++ {
		start, end := mediastream.ChunkRange(i, int64(len(plaintext)))
		if err := file.WriteChunk(i, plaintext[start:end]); err != nil {
			t.Fatalf("write chunk %d: %v", i, err)
		}
	}
	t.Cleanup(func() { file.Close() })

	client.mediaStreamMu.Lock()
	defer client.mediaStreamMu.Unlock()
	if client.mediaStreams == nil {
		client.mediaStreams = make(map[string]*mediaStreamEntry)
	}
	client.mediaStreams[messageID] = &mediaStreamEntry{
		stream:   mediastream.NewForTest(file),
		partPath: partPath,
		mime:     "video/mp4",
	}
}

func get(t *testing.T, url string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, values := range header {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestMediaServerServesRanges is what the player depends on: byte ranges, a
// 206, and the right bytes.
func TestMediaServerServesRanges(t *testing.T) {
	client := newMediaServerClient(t)
	plaintext := bytes.Repeat([]byte("whatevr"), 40000)
	registerFakeStream(t, client, "msg-1", plaintext)

	url := client.mediaStreamEndpoint("msg-1")
	if url == "" {
		t.Fatal("media stream endpoint is empty")
	}

	full := get(t, url, nil)
	if full.StatusCode != http.StatusOK {
		t.Fatalf("full request status = %d, want 200", full.StatusCode)
	}
	body, err := io.ReadAll(full.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, plaintext) {
		t.Fatal("full response body differs from the media")
	}

	ranged := get(t, url, http.Header{"Range": {"bytes=100000-100099"}})
	if ranged.StatusCode != http.StatusPartialContent {
		t.Fatalf("ranged request status = %d, want 206", ranged.StatusCode)
	}
	part, err := io.ReadAll(ranged.Body)
	if err != nil {
		t.Fatalf("read ranged body: %v", err)
	}
	if !bytes.Equal(part, plaintext[100000:100100]) {
		t.Fatal("ranged response body is not the requested bytes")
	}
	if got := ranged.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want %q", got, "bytes")
	}
	if got := ranged.Header.Get("Content-Type"); got != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", got)
	}
}

// TestMediaServerRejectsBadToken keeps other local processes out: the URL is
// only meaningful with the per-daemon token.
func TestMediaServerRejectsBadToken(t *testing.T) {
	client := newMediaServerClient(t)
	registerFakeStream(t, client, "msg-2", []byte("some media bytes"))

	client.mediaServerMu.Lock()
	addr := client.mediaServerAddr
	client.mediaServerMu.Unlock()

	resp := get(t, "http://"+addr+"/media/msg-2?t=wrong", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// TestMediaServerNotFoundForUnknownMessage covers a URL whose stream has
// already been released.
func TestMediaServerNotFoundForUnknownMessage(t *testing.T) {
	client := newMediaServerClient(t)

	resp := get(t, client.mediaStreamEndpoint("missing"), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestMediaServerKeepsURLAfterPromotion(t *testing.T) {
	client := newMediaServerClient(t)
	plaintext := bytes.Repeat([]byte("completed-video"), 20000)
	registerFakeStream(t, client, "promoted", plaintext)
	url := client.mediaStreamEndpoint("promoted")

	finalPath := filepath.Join(t.TempDir(), "promoted.mp4")
	if err := os.WriteFile(finalPath, plaintext, 0o600); err != nil {
		t.Fatalf("write promoted file: %v", err)
	}
	client.mediaStreamMu.Lock()
	entry := client.mediaStreams["promoted"]
	entry.completedPath = finalPath
	entry.size = int64(len(plaintext))
	client.mediaStreamMu.Unlock()

	resp := get(t, url, http.Header{"Range": {"bytes=1234-1333"}})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("promoted range status = %d, want 206", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read promoted range: %v", err)
	}
	if !bytes.Equal(body, plaintext[1234:1334]) {
		t.Fatal("promoted URL did not serve the completed local file")
	}
}

func TestEnsureMediaStreamDoesNotRelockStreamMutex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	payload, err := proto.Marshal(&waE2E.VideoMessage{
		URL:        proto.String(server.URL),
		MediaKey:   bytes.Repeat([]byte{1}, 32),
		FileLength: proto.Uint64(1024),
		FileSHA256: bytes.Repeat([]byte{2}, 32),
		Mimetype:   proto.String("video/mp4"),
	})
	if err != nil {
		t.Fatalf("marshal video payload: %v", err)
	}

	client := &Client{
		daemon: app.NewDaemon(app.Paths{}),
		log:    waLog.Noop,
		paths:  app.Paths{MediaCacheDir: t.TempDir()},
	}
	message := appstore.Message{
		ID:            "first-stream",
		ChatID:        "chat-1",
		MediaKind:     appstore.MediaKindVideo,
		MediaMimeType: "video/mp4",
		MediaPayload:  payload,
	}

	result := make(chan error, 1)
	go func() {
		_, err := client.ensureMediaStream(message, server.URL, "test-stream", nil)
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ensure media stream: %v", err)
		}
		client.dropMediaStream(message.ID, false)
	case <-time.After(time.Second):
		t.Fatal("ensureMediaStream deadlocked while initializing its HTTP client")
	}
}

// TestMediaStreamMMSTypeCoversEveryKind guards the URL rebuild path: a wrong
// mms-type produces a 404 from the CDN rather than a visible error.
func TestMediaStreamMMSTypeCoversEveryKind(t *testing.T) {
	cases := map[string]string{
		appstore.MediaKindVideo:     "video",
		appstore.MediaKindGIF:       "video",
		appstore.MediaKindVideoNote: "video",
		appstore.MediaKindVoice:     "audio",
		appstore.MediaKindAudio:     "audio",
		appstore.MediaKindDocument:  "document",
		appstore.MediaKindImage:     "image",
	}
	for kind, want := range cases {
		if got := mediaStreamMMSType(kind); got != want {
			t.Errorf("mediaStreamMMSType(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestMediaStreamAppInfoCoversEveryKind guards key derivation: the info string
// is what binds a media key to its type, so a wrong one decrypts to noise.
func TestMediaStreamAppInfoCoversEveryKind(t *testing.T) {
	cases := map[string]string{
		appstore.MediaKindVideo:     "WhatsApp Video Keys",
		appstore.MediaKindGIF:       "WhatsApp Video Keys",
		appstore.MediaKindVideoNote: "WhatsApp Video Keys",
		appstore.MediaKindVoice:     "WhatsApp Audio Keys",
		appstore.MediaKindAudio:     "WhatsApp Audio Keys",
		appstore.MediaKindDocument:  "WhatsApp Document Keys",
		appstore.MediaKindImage:     "WhatsApp Image Keys",
	}
	for kind, want := range cases {
		if got := string(mediaStreamAppInfo(kind)); got != want {
			t.Errorf("mediaStreamAppInfo(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestMediaFileExtensionPrefersDocumentFilename(t *testing.T) {
	cases := []struct {
		name    string
		message appstore.Message
		want    string
	}{
		{
			name:    "document keeps its own extension",
			message: appstore.Message{MediaKind: appstore.MediaKindDocument, MediaFileName: "report.pdf", MediaMimeType: "application/pdf"},
			want:    ".pdf",
		},
		{
			name:    "document without a filename falls back to its mime",
			message: appstore.Message{MediaKind: appstore.MediaKindDocument, MediaMimeType: "application/pdf"},
			want:    ".pdf",
		},
		{
			name:    "unknown document type is opaque, not a jpeg",
			message: appstore.Message{MediaKind: appstore.MediaKindDocument, MediaMimeType: "application/x-thing"},
			want:    ".bin",
		},
		{
			name:    "voice note keeps its container",
			message: appstore.Message{MediaKind: appstore.MediaKindVoice, MediaMimeType: "audio/ogg; codecs=opus"},
			want:    ".ogg",
		},
		{
			name:    "video",
			message: appstore.Message{MediaKind: appstore.MediaKindVideo, MediaMimeType: "video/mp4"},
			want:    ".mp4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaFileExtension(tc.message); got != tc.want {
				t.Fatalf("mediaFileExtension() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMediaServerShutdownReleasesStreams(t *testing.T) {
	client := newMediaServerClient(t)
	registerFakeStream(t, client, "msg-3", []byte("bytes"))

	client.StopMediaServer()

	client.mediaStreamMu.Lock()
	remaining := len(client.mediaStreams)
	client.mediaStreamMu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d streams still resident after shutdown", remaining)
	}

	// A second stop must not panic on the already-closed server.
	client.StopMediaServer()
	time.Sleep(10 * time.Millisecond)
}
