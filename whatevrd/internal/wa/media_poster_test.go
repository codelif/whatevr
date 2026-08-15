package wa

import (
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	appstore "whatevrd/internal/store"
)

func savePosterTestMessage(t *testing.T, db *appstore.DB, id, kind, localPath, thumbnailPath string, at int64) appstore.Message {
	t.Helper()
	saved, err := db.SaveMediaMessage(context.Background(), appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:        id,
			ChatID:    "chat-1",
			SenderID:  "sender-1",
			Timestamp: time.Unix(at, 0),
		},
		MediaKind:               kind,
		MediaMimeType:           "video/mp4",
		MediaLocalPath:          localPath,
		MediaThumbnailLocalPath: thumbnailPath,
	})
	if err != nil {
		t.Fatalf("save poster test message: %v", err)
	}
	return saved.Message
}

func TestDeriveVideoPosterPersistsAtomicOutput(t *testing.T) {
	client, db := newTestMediaClient(t)
	defer db.Close()
	client.log = waLog.Noop

	mediaPath := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(mediaPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("write media: %v", err)
	}
	message := savePosterTestMessage(t, db, "chat-1:clip", appstore.MediaKindVideo, mediaPath, "/cache/sender-thumb.jpg", 100)

	var extractionOutput string
	client.posterExtractor = func(_ context.Context, source, output string) error {
		if source != mediaPath {
			t.Fatalf("extractor source = %q, want %q", source, mediaPath)
		}
		extractionOutput = output
		return os.WriteFile(output, []byte("jpeg poster"), 0o600)
	}
	client.deriveAndPublishVideoPoster(context.Background(), message)

	wantPath := videoPosterPath(mediaPath)
	if extractionOutput != wantPath {
		t.Fatalf("extractor output = %q, want %q", extractionOutput, wantPath)
	}
	updated, err := db.GetMessage(context.Background(), message.ID)
	if err != nil {
		t.Fatalf("get updated message: %v", err)
	}
	if updated.MediaThumbnailLocalPath != wantPath {
		t.Fatalf("thumbnail path = %q, want %q", updated.MediaThumbnailLocalPath, wantPath)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat poster: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("poster permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestDeriveVideoPosterFailureKeepsSenderThumbnail(t *testing.T) {
	client, db := newTestMediaClient(t)
	defer db.Close()
	client.log = waLog.Noop

	mediaPath := filepath.Join(t.TempDir(), "broken.mp4")
	if err := os.WriteFile(mediaPath, []byte("broken"), 0o600); err != nil {
		t.Fatalf("write media: %v", err)
	}
	const senderThumbnail = "/cache/sender-thumb.jpg"
	message := savePosterTestMessage(t, db, "chat-1:broken", appstore.MediaKindGIF, mediaPath, senderThumbnail, 100)
	client.posterExtractor = func(context.Context, string, string) error {
		return context.DeadlineExceeded
	}

	client.deriveAndPublishVideoPoster(context.Background(), message)
	updated, err := db.GetMessage(context.Background(), message.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if updated.MediaThumbnailLocalPath != senderThumbnail {
		t.Fatalf("thumbnail path = %q, want sender thumbnail %q", updated.MediaThumbnailLocalPath, senderThumbnail)
	}
	if _, err := os.Stat(videoPosterPath(mediaPath)); !os.IsNotExist(err) {
		t.Fatalf("failed extraction left output behind: %v", err)
	}
}

func TestPosterQueuePrioritizesNewDownloads(t *testing.T) {
	client := &Client{}
	oldest := appstore.Message{ID: "old", MediaKind: appstore.MediaKindVideo, MediaLocalPath: "/cache/old.mp4"}
	newest := appstore.Message{ID: "new", MediaKind: appstore.MediaKindVideo, MediaLocalPath: "/cache/new.mp4"}
	client.queueVideoPoster(oldest, posterPriorityBackfill)
	client.queueVideoPoster(newest, posterPriorityDownload)

	first, ok := client.takeVideoPoster()
	if !ok || first.ID != newest.ID {
		t.Fatalf("first poster = %q, want new download", first.ID)
	}
	second, ok := client.takeVideoPoster()
	if !ok || second.ID != oldest.ID {
		t.Fatalf("second poster = %q, want backfill", second.ID)
	}
}

// A poster derived under an older version is superseded, not kept: the message
// row moves to the new path first, then the old file goes. That ordering is
// what makes an interrupted upgrade leave a working poster rather than a row
// naming a file that has been deleted.
func TestDeriveVideoPosterSupersedesTheOlderOutput(t *testing.T) {
	client, db := newTestMediaClient(t)
	defer db.Close()
	client.log = waLog.Noop

	mediaPath := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(mediaPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("write media: %v", err)
	}
	stale := supersededVideoPosterPaths(mediaPath)
	if len(stale) == 0 {
		t.Fatal("no superseded poster paths to test")
	}
	for _, path := range stale {
		if err := os.WriteFile(path, []byte("old poster"), 0o600); err != nil {
			t.Fatalf("write superseded poster: %v", err)
		}
	}
	message := savePosterTestMessage(t, db, "chat-1:clip", appstore.MediaKindVideo, mediaPath, stale[0], 100)

	client.posterExtractor = func(_ context.Context, _, output string) error {
		return os.WriteFile(output, []byte("jpeg poster"), 0o600)
	}
	client.deriveAndPublishVideoPoster(context.Background(), message)

	updated, err := db.GetMessage(context.Background(), message.ID)
	if err != nil {
		t.Fatalf("get updated message: %v", err)
	}
	if updated.MediaThumbnailLocalPath != videoPosterPath(mediaPath) {
		t.Fatalf("thumbnail path = %q, want the current version %q",
			updated.MediaThumbnailLocalPath, videoPosterPath(mediaPath))
	}
	for _, path := range stale {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("superseded poster %q still on disk (err = %v)", path, err)
		}
	}
}

// A clip that opens on black is ordinary, and frame 0 of one says nothing about
// it. The extraction seeks past the opening and lets ffmpeg pick the least
// typical frame of a batch.
func TestExtractVideoPosterSkipsABlackOpening(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "fade.mp4")
	if output, err := exec.Command(ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:r=25:d=2",
		"-f", "lavfi", "-i", "color=c=red:s=320x240:r=25:d=4",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0",
		"-pix_fmt", "yuv420p", source,
	).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot create test video: %v: %s", err, output)
	}

	poster := filepath.Join(dir, "poster.jpg")
	if err := extractVideoPoster(context.Background(), source, poster); err != nil {
		t.Fatalf("extractVideoPoster() error = %v", err)
	}
	file, err := os.Open(poster)
	if err != nil {
		t.Fatalf("open poster: %v", err)
	}
	defer file.Close()
	decoded, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("decode poster: %v", err)
	}
	bounds := decoded.Bounds()
	r, g, b, _ := decoded.At(bounds.Dx()/2, bounds.Dy()/2).RGBA()
	if r>>8 < 128 || g>>8 > 96 || b>>8 > 96 {
		t.Fatalf("poster centre = rgb(%d,%d,%d), want the red body of the clip rather than its black opening",
			r>>8, g>>8, b>>8)
	}
}

func TestExtractVideoPosterCapsLongestEdge(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	if output, err := exec.Command(ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=1920x1080:d=0.1",
		"-frames:v", "1", source,
	).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot create test video: %v: %s", err, output)
	}
	poster := filepath.Join(dir, "poster.jpg")
	if err := extractVideoPoster(context.Background(), source, poster); err != nil {
		t.Fatalf("extractVideoPoster() error = %v", err)
	}
	file, err := os.Open(poster)
	if err != nil {
		t.Fatalf("open poster: %v", err)
	}
	defer file.Close()
	config, err := jpeg.DecodeConfig(file)
	if err != nil {
		t.Fatalf("decode poster config: %v", err)
	}
	if config.Width != 1280 || config.Height != 720 {
		t.Fatalf("poster dimensions = %dx%d, want 1280x720", config.Width, config.Height)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat poster: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("poster permissions = %o, want 600", info.Mode().Perm())
	}
}
