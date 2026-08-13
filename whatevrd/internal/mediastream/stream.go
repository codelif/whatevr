package mediastream

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Source is everything needed to fetch and decrypt one message's media.
type Source struct {
	// URL is the fully-formed CDN URL from the message payload.
	URL string
	// Keys is the expanded media key.
	Keys Keys
	// Sidecar is the optional per-chunk MAC table.
	Sidecar []byte
	// PlaintextLen is the declared file length. Streaming needs it: without a
	// length there is no chunk count and no way to strip the final padding.
	PlaintextLen int64
	// FileSHA256 is the plaintext hash the completed file must match.
	FileSHA256 []byte
	// Mime is carried through to the HTTP response so the player can pick a
	// demuxer without sniffing.
	Mime string
}

// Valid reports whether this source can be streamed at all. Anything it
// rejects has to go through an ordinary whole-file download instead.
func (s Source) Valid() error {
	switch {
	case s.URL == "":
		return errors.New("mediastream: message carries no media URL")
	case s.PlaintextLen <= 0:
		return errors.New("mediastream: message carries no file length")
	case len(s.FileSHA256) != sha256.Size:
		return errors.New("mediastream: message carries no file hash")
	case len(s.Keys.CipherKey) != 32:
		return errors.New("mediastream: media key did not expand")
	}
	return nil
}

// ErrRangeUnsupported means the CDN answered a ranged request with the whole
// body. Streaming is impossible against such a server, so the caller falls back
// to a whole-file download.
var ErrRangeUnsupported = errors.New("mediastream: server ignored the byte range")

// prefetchChunks is how far ahead of the read head the fetcher stays. Eight
// chunks is half a megabyte: enough that a player's demuxer never starves on a
// steady read, small enough that a seek does not first have to drain a long
// queue of now-useless requests.
const prefetchChunks = 8

// Stream is one message's in-progress fetch. It owns a goroutine that pulls
// chunks in ranges, decrypts them and fills the sparse file, prioritising
// whatever the reader is currently looking at and otherwise working forward to
// completion, so a stream that is opened once ends up as a complete cache file
// even if the viewer stops watching.
type Stream struct {
	source    Source
	file      *SparseFile
	partPath  string
	decryptor *Decryptor
	verifier  *Verifier
	client    *http.Client

	// progress is called as chunks land, and done exactly once when the fetch
	// finishes. err is nil on a verified, complete file.
	progress func(receivedBytes, totalBytes int64)
	done     func(err error)

	mu       sync.Mutex
	readHead int
	wake     chan struct{}

	ctx    context.Context
	cancel context.CancelFunc

	finishOnce sync.Once
	finished   chan struct{}
	finishErr  error
}

// New starts fetching source into the partial file at partPath. The returned
// Stream is usable immediately; readers block on the chunks they need.
func New(source Source, partPath string, client *http.Client, progress func(received, total int64), done func(error)) (*Stream, error) {
	if err := source.Valid(); err != nil {
		return nil, err
	}
	decryptor, err := NewDecryptor(source.Keys, source.PlaintextLen)
	if err != nil {
		return nil, err
	}
	file, err := OpenSparseFile(partPath, source.PlaintextLen)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	if progress == nil {
		progress = func(int64, int64) {}
	}
	if done == nil {
		done = func(error) {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Stream{
		source:    source,
		file:      file,
		partPath:  partPath,
		decryptor: decryptor,
		verifier:  NewVerifier(source.Keys, source.Sidecar),
		client:    client,
		progress:  progress,
		done:      done,
		wake:      make(chan struct{}, 1),
		ctx:       ctx,
		cancel:    cancel,
		finished:  make(chan struct{}),
	}
	go s.run()
	return s, nil
}

// NewForTest wraps an already-populated sparse file as a finished stream, so
// callers can exercise everything downstream of the fetch (the range server,
// promotion into the cache) without a CDN.
func NewForTest(file *SparseFile) *Stream {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Stream{
		source:   Source{PlaintextLen: file.Size()},
		file:     file,
		client:   http.DefaultClient,
		progress: func(int64, int64) {},
		done:     func(error) {},
		wake:     make(chan struct{}, 1),
		ctx:      ctx,
		cancel:   cancel,
		finished: make(chan struct{}),
	}
	s.finish(nil)
	return s
}

// Size is the full plaintext length.
func (s *Stream) Size() int64 { return s.source.PlaintextLen }

// Mime is the message's declared content type.
func (s *Stream) Mime() string { return s.source.Mime }

// Complete reports whether every chunk has landed.
func (s *Stream) Complete() bool { return s.file.Complete() }

// ReadyBytes is how much of the plaintext is already on disk.
func (s *Stream) ReadyBytes() int64 { return s.file.PresentBytes() }

// Finished is closed once the fetch has stopped, successfully or not.
func (s *Stream) Finished() <-chan struct{} { return s.finished }

// Err is the fetch's outcome, valid once Finished is closed.
func (s *Stream) Err() error {
	<-s.finished
	return s.finishErr
}

// Cancel stops fetching. The partial file and its index stay on disk, so a
// later stream resumes where this one stopped.
func (s *Stream) Cancel() {
	s.cancel()
	s.finish(context.Canceled)
}

// Close cancels the fetch and releases the file handle.
func (s *Stream) Close() {
	s.Cancel()
	s.file.Close()
}

// Discard throws away everything fetched so far, for media that turned out to
// be corrupt or unfetchable.
func (s *Stream) Discard() {
	s.cancel()
	s.finish(context.Canceled)
	s.file.Discard(s.partPath)
}

// SeekTo moves the read head so the fetcher prioritises the chunk containing
// offset. This is what makes seeking in a half-fetched video cheap: the next
// range request starts where the viewer is looking, not where the sequential
// fill happened to be.
func (s *Stream) SeekTo(offset int64) {
	if offset < 0 {
		offset = 0
	}
	index := int(offset / ChunkSize)

	s.mu.Lock()
	changed := s.readHead != index
	s.readHead = index
	s.mu.Unlock()

	if changed {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// WaitFor blocks until the chunk containing offset is on disk.
func (s *Stream) WaitFor(ctx context.Context, offset int64) error {
	index := int(offset / ChunkSize)
	if s.file.Has(index) {
		return nil
	}
	s.SeekTo(offset)

	// Surface a fetch failure to a waiting reader rather than blocking it until
	// the request is abandoned.
	select {
	case <-s.finished:
		if s.finishErr != nil {
			return s.finishErr
		}
	default:
	}
	return s.file.WaitFor(index, ctx.Done())
}

// ReadAt reads plaintext that has already arrived.
func (s *Stream) ReadAt(p []byte, off int64) (int, error) { return s.file.ReadAt(p, off) }

// run is the fetch loop: fill from the read head, then sweep whatever is still
// missing, so the file always completes even though the reader only ever asks
// for part of it.
func (s *Stream) run() {
	chunks := ChunkCount(s.source.PlaintextLen)
	for {
		if s.ctx.Err() != nil {
			s.finish(s.ctx.Err())
			return
		}

		s.mu.Lock()
		head := s.readHead
		s.mu.Unlock()

		next, ok := s.file.MissingFrom(head)
		if !ok {
			// Nothing missing ahead of the reader; fall back to filling holes
			// left behind by seeks.
			next, ok = s.file.MissingFrom(0)
		}
		if !ok {
			s.finish(s.verifyComplete())
			return
		}

		last := next + prefetchChunks - 1
		if last >= chunks {
			last = chunks - 1
		}
		// Stop the span at the first chunk we already have, so a seek back into
		// fetched territory does not refetch it.
		for i := next; i <= last; i++ {
			if s.file.Has(i) {
				last = i - 1
				break
			}
		}
		if last < next {
			last = next
		}

		if err := s.fetchSpan(next, last); err != nil {
			if errors.Is(err, context.Canceled) {
				s.finish(err)
				return
			}
			s.finish(err)
			return
		}
		s.progress(s.file.PresentBytes(), s.source.PlaintextLen)
	}
}

// fetchSpan pulls one contiguous run of chunks in a single ranged request,
// verifies each against the sidecar, decrypts it and stores it.
func (s *Stream) fetchSpan(first, last int) error {
	start, end := CipherRange(first, last, s.source.PlaintextLen)
	body, err := s.fetchRange(start, end)
	if err != nil {
		return err
	}

	chunks := ChunkCount(s.source.PlaintextLen)
	// leadOffset is where chunk `first` begins inside body: one block in,
	// unless this span starts at the very beginning of the file.
	leadOffset := 0
	if first > 0 {
		leadOffset = aesBlockSize
	}

	for index := first; index <= last; index++ {
		chunkStart := leadOffset + (index-first)*ChunkSize
		chunkEnd := chunkStart + ChunkSize
		if index == chunks-1 || chunkEnd > len(body) {
			chunkEnd = len(body)
		}
		if chunkStart >= chunkEnd {
			return fmt.Errorf("mediastream: short response for chunk %d", index)
		}

		iv := s.source.Keys.IV
		if index > 0 {
			if chunkStart < aesBlockSize {
				return fmt.Errorf("mediastream: response for chunk %d is missing its iv", index)
			}
			iv = body[chunkStart-aesBlockSize : chunkStart]
		}
		trailing := []byte(nil)
		if chunkEnd+aesBlockSize <= len(body) {
			trailing = body[chunkEnd : chunkEnd+aesBlockSize]
		}
		// The final chunk is skipped: it carries the PKCS#7 padding block, and
		// implementations disagree on whether the sidecar's last entry covers
		// that block. Guessing wrong would reject a perfectly good file, and the
		// whole-file SHA-256 checked at completion covers these bytes anyway.
		if index < chunks-1 {
			if err := s.verifier.Verify(index, iv, body[chunkStart:chunkEnd], trailing); err != nil {
				return err
			}
		}

		plaintext, err := s.decryptor.DecryptChunk(index, iv, body[chunkStart:chunkEnd])
		if err != nil {
			return err
		}
		if err := s.file.WriteChunk(index, plaintext); err != nil {
			return err
		}
	}
	return nil
}

// fetchRange performs one HTTP range request for [start, end).
func (s *Stream) fetchRange(start, end int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.source.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("mediastream: build request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
	req.Header.Set("Origin", "https://web.whatsapp.com")
	req.Header.Set("Referer", "https://web.whatsapp.com/")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mediastream: fetch range: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		// The server sent the whole file for a ranged request, so it cannot be
		// streamed. Say so plainly instead of quietly decrypting the wrong
		// offsets.
		return nil, ErrRangeUnsupported
	default:
		return nil, fmt.Errorf("mediastream: fetch range: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, end-start))
	if err != nil {
		return nil, fmt.Errorf("mediastream: read range: %w", err)
	}
	if int64(len(body)) != end-start {
		return nil, fmt.Errorf("mediastream: range returned %d bytes, want %d", len(body), end-start)
	}
	return body, nil
}

// verifyComplete hashes the assembled plaintext against the hash the message
// declared. This is the check that makes a streamed file as trustworthy as a
// whole-file download, and it is also why a sidecar is optional.
func (s *Stream) verifyComplete() error {
	hash := sha256.New()
	buf := make([]byte, 256*1024)
	for off := int64(0); off < s.source.PlaintextLen; {
		n, err := s.file.ReadAt(buf, off)
		if n > 0 {
			hash.Write(buf[:n])
			off += int64(n)
		}
		if err != nil && err != io.EOF {
			return fmt.Errorf("mediastream: verify: %w", err)
		}
		if n == 0 {
			break
		}
	}
	if got := hash.Sum(nil); string(got) != string(s.source.FileSHA256) {
		return errors.New("mediastream: assembled file does not match the message hash")
	}
	return nil
}

func (s *Stream) finish(err error) {
	s.finishOnce.Do(func() {
		s.finishErr = err
		close(s.finished)
		s.done(err)
	})
}
