package wa

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"

	"whatevrd/internal/app"
	"whatevrd/internal/mediastream"
	appstore "whatevrd/internal/store"
)

// mediaStreamHost is the CDN host used when a message's own URL is gone (a
// media retry clears it and leaves only a direct path). It is the host
// whatsmeow's media connection hands back in practice.
const mediaStreamHost = "mmg.whatsapp.net"

// mediaStreamIdleTimeout is how long a finished stream stays resident before
// its file handle is released. Long enough that pausing and resuming a video
// costs nothing, short enough that a browsed-through gallery does not pin a
// handle per item.
const mediaStreamIdleTimeout = 2 * time.Minute

type mediaStreamEntry struct {
	stream   *mediastream.Stream
	partPath string
	// finalPath is where the file is renamed once every chunk has landed and
	// the hash checks out.
	finalPath string
	mime      string
	idleTimer *time.Timer
}

// StreamMessageMedia starts (or joins) a ranged fetch for a message's media and
// returns a loopback URL a player can open immediately. The fetch continues to
// completion in the background, so a message played once ends up as an ordinary
// complete cache file, at which point the message row upserts with media.path
// and this URL stops being needed.
func (c *Client) StreamMessageMedia(ctx context.Context, messageID string) (app.MediaStream, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return app.MediaStream{}, app.NewCommandError(app.CommandErrorInvalidArgument, "message_id is required")
	}

	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return app.MediaStream{}, err
	}

	// An already-downloaded message needs no server at all; the caller should
	// use the path from the message row.
	if message.MediaLocalPath != "" {
		if _, err := os.Stat(message.MediaLocalPath); err == nil {
			return app.MediaStream{}, app.NewCommandError(app.CommandErrorRejected, "media is already downloaded")
		}
	}
	if len(message.MediaPayload) == 0 {
		return app.MediaStream{}, app.NewCommandError(app.CommandErrorNotFound, "media is not available for download")
	}

	url, err := c.mediaStreamURL(ctx, message)
	if err != nil {
		return app.MediaStream{}, err
	}

	entry, err := c.ensureMediaStream(message, url)
	if err != nil {
		return app.MediaStream{}, err
	}

	return app.MediaStream{
		URL:          c.mediaStreamEndpoint(message.ID),
		Mime:         entry.mime,
		SizeBytes:    uint64(entry.stream.Size()),
		DurationSecs: message.MediaDurationSecs,
	}, nil
}

// mediaStreamURL resolves where the encrypted bytes live. The payload's own URL
// is preferred; a message whose URL was cleared by a media retry is rebuilt
// from its direct path, the same way whatsmeow builds one.
func (c *Client) mediaStreamURL(ctx context.Context, message appstore.Message) (string, error) {
	media, err := downloadableMediaMessage(message)
	if err != nil {
		return "", err
	}
	if url := strings.TrimSpace(media.GetURL()); url != "" && !isPlaceholderMediaURL(url) {
		return url, nil
	}
	directPath := strings.TrimSpace(media.GetDirectPath())
	if directPath == "" || !strings.HasPrefix(directPath, "/") {
		return "", app.NewCommandError(app.CommandErrorRejected, "media has no streamable location")
	}
	return fmt.Sprintf(
		"https://%s%s&hash=%s&mms-type=%s&__wa-mms=",
		mediaStreamHost,
		directPath,
		urlSafeBase64(media.GetFileEncSHA256()),
		mediaStreamMMSType(message.MediaKind),
	), nil
}

func mediaStreamMMSType(mediaKind string) string {
	switch mediaKind {
	case appstore.MediaKindVideo, appstore.MediaKindGIF, appstore.MediaKindVideoNote:
		return "video"
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		return "audio"
	case appstore.MediaKindDocument:
		return "document"
	default:
		return "image"
	}
}

// mediaStreamAppInfo is the HKDF info string for a kind, which is what ties the
// media key to this particular media type.
func mediaStreamAppInfo(mediaKind string) whatsmeow.MediaType {
	switch mediaKind {
	case appstore.MediaKindVideo, appstore.MediaKindGIF, appstore.MediaKindVideoNote:
		return whatsmeow.MediaVideo
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		return whatsmeow.MediaAudio
	case appstore.MediaKindDocument:
		return whatsmeow.MediaDocument
	default:
		return whatsmeow.MediaImage
	}
}

// ensureMediaStream returns the live stream for a message, starting one if
// needed. Two viewers of the same message share a single fetch.
func (c *Client) ensureMediaStream(message appstore.Message, url string) (*mediaStreamEntry, error) {
	c.mediaStreamMu.Lock()
	defer c.mediaStreamMu.Unlock()

	if c.mediaStreams == nil {
		c.mediaStreams = make(map[string]*mediaStreamEntry)
	}
	if entry, ok := c.mediaStreams[message.ID]; ok {
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
			entry.idleTimer = nil
		}
		return entry, nil
	}

	media, err := downloadableMediaMessage(message)
	if err != nil {
		return nil, err
	}
	keys, err := mediastream.DeriveKeys(media.GetMediaKey(), string(mediaStreamAppInfo(message.MediaKind)))
	if err != nil {
		return nil, app.NewCommandError(app.CommandErrorRejected, "media cannot be streamed: %v", err)
	}

	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", message.ChatID)
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return nil, app.NewCommandError(app.CommandErrorInternal, "create media cache directory: %v", err)
	}
	finalPath := filepath.Join(mediaDir, safeMediaFileName(message.ID, mediaFileExtension(message)))

	source := mediastream.Source{
		URL:          url,
		Keys:         keys,
		Sidecar:      streamingSidecar(message),
		PlaintextLen: int64(media.GetFileLength()),
		FileSHA256:   media.GetFileSHA256(),
		Mime:         message.MediaMimeType,
	}
	if err := source.Valid(); err != nil {
		return nil, app.NewCommandError(app.CommandErrorRejected, "media cannot be streamed: %v", err)
	}
	if source.PlaintextLen > maxInboundMediaBytes {
		return nil, app.NewCommandError(app.CommandErrorRejected, "media size must be between 1 byte and %d MiB", maxInboundMediaBytes/(1024*1024))
	}

	messageID, chatID := message.ID, message.ChatID
	stream, err := mediastream.New(
		source,
		finalPath+".part",
		c.mediaStreamClient(),
		func(received, total int64) {
			c.daemon.PublishMediaDownloadChanged(messageID, chatID, true, "", uint64(received), uint64(total))
		},
		func(err error) {
			c.finishMediaStream(messageID, err)
		},
	)
	if err != nil {
		return nil, app.NewCommandError(app.CommandErrorInternal, "start media stream: %v", err)
	}

	entry := &mediaStreamEntry{
		stream:    stream,
		partPath:  finalPath + ".part",
		finalPath: finalPath,
		mime:      message.MediaMimeType,
	}
	c.mediaStreams[message.ID] = entry
	c.daemon.PublishMediaDownloadChanged(messageID, chatID, true, "", uint64(stream.ReadyBytes()), uint64(stream.Size()))
	return entry, nil
}

// streamingSidecar pulls the per-chunk MAC table out of the stored payload.
// Only video and audio carry one.
func streamingSidecar(message appstore.Message) []byte {
	decoded, err := mediaPayloadMessage(message)
	if err != nil {
		return nil
	}
	switch m := decoded.(type) {
	case *waE2E.VideoMessage:
		return m.GetStreamingSidecar()
	case *waE2E.AudioMessage:
		return m.GetStreamingSidecar()
	default:
		return nil
	}
}

func (c *Client) mediaStreamClient() *http.Client {
	c.mediaStreamMu.Lock()
	defer c.mediaStreamMu.Unlock()
	if c.mediaStreamHTTP == nil {
		c.mediaStreamHTTP = &http.Client{Timeout: 2 * time.Minute}
	}
	return c.mediaStreamHTTP
}

// finishMediaStream promotes a completed stream into an ordinary cache entry:
// the partial file becomes the real file and the message row upserts with its
// path, after which nothing consults the stream again. A failed stream keeps
// whatever it fetched (so a retry resumes) but records the error.
func (c *Client) finishMediaStream(messageID string, streamErr error) {
	c.mediaStreamMu.Lock()
	entry, ok := c.mediaStreams[messageID]
	c.mediaStreamMu.Unlock()
	if !ok {
		return
	}

	ctx := c.backgroundContext()
	if streamErr != nil {
		if errors.Is(streamErr, context.Canceled) {
			return
		}
		c.log.Warnf("Media stream for %s failed: %v", messageID, streamErr)
		c.dropMediaStream(messageID, errors.Is(streamErr, mediastream.ErrRangeUnsupported))
		// A stream that cannot work falls back to the whole-file download,
		// which is also what surfaces the error on the message row.
		if _, err := c.DownloadMessageMedia(ctx, messageID); err != nil {
			c.log.Warnf("Fallback download for %s failed: %v", messageID, err)
		}
		return
	}

	if err := os.Rename(entry.partPath, entry.finalPath); err != nil {
		c.log.Warnf("Failed to promote streamed media for %s: %v", messageID, err)
		c.dropMediaStream(messageID, true)
		return
	}
	os.Remove(entry.partPath + ".idx")

	updated, err := c.store.UpdateMessageMediaLocalPath(ctx, messageID, entry.finalPath)
	if err != nil {
		c.log.Warnf("Failed to record streamed media path for %s: %v", messageID, err)
		c.dropMediaStream(messageID, false)
		return
	}
	c.daemon.PublishMediaDownloadChanged(messageID, updated.ChatID, false, "", uint64(entry.stream.Size()), uint64(entry.stream.Size()))
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
	c.maybeDeriveVoiceWaveform(ctx, updated)

	// Keep the entry briefly so a player mid-request can finish reading, then
	// let it go: the file is on disk and future plays use the path.
	c.scheduleMediaStreamRelease(messageID)
}

// scheduleMediaStreamRelease drops a finished stream after an idle period.
func (c *Client) scheduleMediaStreamRelease(messageID string) {
	c.mediaStreamMu.Lock()
	defer c.mediaStreamMu.Unlock()
	entry, ok := c.mediaStreams[messageID]
	if !ok {
		return
	}
	if entry.idleTimer != nil {
		entry.idleTimer.Stop()
	}
	entry.idleTimer = time.AfterFunc(mediaStreamIdleTimeout, func() {
		c.dropMediaStream(messageID, false)
	})
}

// dropMediaStream stops and forgets a stream. discardPartial also removes the
// bytes fetched so far, for a stream whose partial file cannot be trusted or
// resumed.
func (c *Client) dropMediaStream(messageID string, discardPartial bool) {
	c.mediaStreamMu.Lock()
	entry, ok := c.mediaStreams[messageID]
	if ok {
		delete(c.mediaStreams, messageID)
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
		}
	}
	c.mediaStreamMu.Unlock()
	if !ok {
		return
	}
	if discardPartial {
		entry.stream.Discard()
		return
	}
	entry.stream.Close()
}

// closeMediaStreams stops every in-flight stream, for daemon shutdown.
func (c *Client) closeMediaStreams() {
	c.mediaStreamMu.Lock()
	streams := make([]*mediaStreamEntry, 0, len(c.mediaStreams))
	for id, entry := range c.mediaStreams {
		streams = append(streams, entry)
		delete(c.mediaStreams, id)
	}
	c.mediaStreamMu.Unlock()
	for _, entry := range streams {
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
		}
		entry.stream.Close()
	}
}

// --- loopback range server ------------------------------------------------

// mediaStreamEndpoint is the URL a frontend hands to its player.
func (c *Client) mediaStreamEndpoint(messageID string) string {
	c.mediaServerMu.Lock()
	defer c.mediaServerMu.Unlock()
	if c.mediaServerAddr == "" {
		return ""
	}
	return fmt.Sprintf("http://%s/media/%s?t=%s", c.mediaServerAddr, urlPathEscape(messageID), c.mediaServerToken)
}

// StartMediaServer binds the loopback range server. It listens on 127.0.0.1
// with an ephemeral port and a fresh token per daemon start, so a URL handed
// out by one daemon process is meaningless to the next. Media still never
// crosses the protocol socket: this serves the same cache file the protocol
// otherwise names by path, to a player that cannot read a half-written file.
func (c *Client) StartMediaServer() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("bind media stream server: %w", err)
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		listener.Close()
		return fmt.Errorf("generate media stream token: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/media/", c.serveMediaStream)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	c.mediaServerMu.Lock()
	c.mediaServerAddr = listener.Addr().String()
	c.mediaServerToken = hex.EncodeToString(tokenBytes)
	c.mediaServer = server
	c.mediaServerMu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.log.Warnf("Media stream server stopped: %v", err)
		}
	}()
	return nil
}

// StopMediaServer shuts the range server down and cancels every in-flight
// fetch.
func (c *Client) StopMediaServer() {
	c.mediaServerMu.Lock()
	server := c.mediaServer
	c.mediaServer = nil
	c.mediaServerAddr = ""
	c.mediaServerToken = ""
	c.mediaServerMu.Unlock()

	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	c.closeMediaStreams()
}

func (c *Client) serveMediaStream(w http.ResponseWriter, r *http.Request) {
	c.mediaServerMu.Lock()
	token := c.mediaServerToken
	c.mediaServerMu.Unlock()

	if token == "" || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	messageID := strings.TrimPrefix(r.URL.Path, "/media/")
	if messageID == "" {
		http.NotFound(w, r)
		return
	}

	c.mediaStreamMu.Lock()
	entry, ok := c.mediaStreams[messageID]
	c.mediaStreamMu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	if entry.mime != "" {
		w.Header().Set("Content-Type", entry.mime)
	}
	// http.ServeContent handles range parsing, 206 responses and Content-Range;
	// the reader below is what makes those ranges wait for bytes that have not
	// arrived yet.
	http.ServeContent(w, r, "", time.Time{}, &mediaStreamReader{
		stream: entry.stream,
		ctx:    r.Context(),
	})
}

// mediaStreamReader is a seekable view over an in-progress stream. A read of
// bytes that have not arrived moves the fetcher's read head there and blocks
// until they do, which is exactly what a player's demuxer expects from a slow
// network file.
type mediaStreamReader struct {
	stream *mediastream.Stream
	ctx    context.Context
	offset int64
}

func (r *mediaStreamReader) Read(p []byte) (int, error) {
	if r.offset >= r.stream.Size() {
		return 0, io.EOF
	}
	if err := r.stream.WaitFor(r.ctx, r.offset); err != nil {
		return 0, err
	}
	n, err := r.stream.ReadAt(p, r.offset)
	r.offset += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

func (r *mediaStreamReader) Seek(offset int64, whence int) (int64, error) {
	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = r.offset + offset
	case io.SeekEnd:
		target = r.stream.Size() + offset
	default:
		return 0, fmt.Errorf("media stream: invalid whence %d", whence)
	}
	if target < 0 {
		return 0, errors.New("media stream: negative seek")
	}
	r.offset = target
	// Tell the fetcher where the viewer went, so the next range request is the
	// one that is about to be read rather than the sequential next.
	if target < r.stream.Size() {
		r.stream.SeekTo(target)
	}
	return target, nil
}

var _ io.ReadSeeker = (*mediaStreamReader)(nil)

// urlPathEscape keeps a message id usable inside a URL path.
func urlPathEscape(id string) string {
	return strings.NewReplacer("/", "%2F", "?", "%3F", "#", "%23", " ", "%20").Replace(id)
}

// urlSafeBase64 matches the hash encoding WhatsApp expects in a media URL.
func urlSafeBase64(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return base64.URLEncoding.EncodeToString(data)
}
