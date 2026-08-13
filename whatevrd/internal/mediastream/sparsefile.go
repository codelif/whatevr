package mediastream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// SparseFile is a partially-fetched media file: the plaintext bytes that have
// arrived, written at their real offsets, plus a record of which chunks those
// are. It is the single representation of "downloading" and "downloaded": when
// every chunk is present the file is byte-identical to a completed download and
// is simply renamed into place.
//
// The chunk record lives beside the data as an index file, so a daemon restart
// can resume rather than refetch. An index that does not match the expected
// chunk count is discarded along with its data file: resuming into a file we
// cannot describe would risk serving stale bytes as fresh ones.
type SparseFile struct {
	mu           sync.RWMutex
	file         *os.File
	indexPath    string
	present      []bool
	presentCount int
	plaintextLen int64
	cond         *sync.Cond
	closed       bool
}

// indexMagic prefixes the index file so a stray file of the right size is not
// mistaken for one of ours.
var indexMagic = []byte("whatevr-mediastream-1\n")

// OpenSparseFile opens (or resumes) the partial file at path. plaintextLen is
// the message's declared file length.
func OpenSparseFile(path string, plaintextLen int64) (*SparseFile, error) {
	if plaintextLen <= 0 {
		return nil, errors.New("mediastream: plaintext length is unknown")
	}
	chunks := ChunkCount(plaintextLen)
	indexPath := path + ".idx"

	present, ok := readIndex(indexPath, chunks)
	if !ok {
		// Either there is no index or it describes a different file. Start over
		// rather than trust bytes we cannot account for.
		os.Remove(path)
		os.Remove(indexPath)
		present = make([]bool, chunks)
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mediastream: open partial file: %w", err)
	}
	if err := file.Truncate(plaintextLen); err != nil {
		file.Close()
		return nil, fmt.Errorf("mediastream: size partial file: %w", err)
	}

	s := &SparseFile{
		file:         file,
		indexPath:    indexPath,
		present:      present,
		plaintextLen: plaintextLen,
	}
	for _, p := range present {
		if p {
			s.presentCount++
		}
	}
	s.cond = sync.NewCond(&s.mu)
	return s, nil
}

func readIndex(path string, chunks int) ([]bool, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) != len(indexMagic)+chunks {
		return nil, false
	}
	if string(data[:len(indexMagic)]) != string(indexMagic) {
		return nil, false
	}
	present := make([]bool, chunks)
	for i, b := range data[len(indexMagic):] {
		present[i] = b == 1
	}
	return present, true
}

// Has reports whether chunk index has been fetched.
func (s *SparseFile) Has(index int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasLocked(index)
}

func (s *SparseFile) hasLocked(index int) bool {
	return index >= 0 && index < len(s.present) && s.present[index]
}

// WriteChunk stores one decrypted chunk and marks it present. Writing a chunk
// that is already present is a no-op, which is what makes overlapping fetches
// harmless.
func (s *SparseFile) WriteChunk(index int, data []byte) error {
	start, end := ChunkRange(index, s.plaintextLen)
	if int64(len(data)) != end-start {
		return fmt.Errorf("mediastream: chunk %d is %d bytes, want %d", index, len(data), end-start)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	if s.hasLocked(index) {
		return nil
	}
	if _, err := s.file.WriteAt(data, start); err != nil {
		return fmt.Errorf("mediastream: write chunk %d: %w", index, err)
	}
	s.present[index] = true
	s.presentCount++
	s.writeIndexLocked()
	s.cond.Broadcast()
	return nil
}

// writeIndexLocked persists the chunk record. It is rewritten whole on every
// chunk: at 64 KiB per chunk even a 2 GiB file has only ~32k chunks, so this is
// a 32 KB write per 64 KB of media, and it keeps resume honest without a
// journal.
func (s *SparseFile) writeIndexLocked() {
	buf := make([]byte, 0, len(indexMagic)+len(s.present))
	buf = append(buf, indexMagic...)
	for _, p := range s.present {
		if p {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	// A lost index only costs a refetch, so a failure here is not fatal.
	_ = os.WriteFile(s.indexPath, buf, 0o600)
}

// Complete reports whether every chunk has arrived.
func (s *SparseFile) Complete() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.presentCount == len(s.present)
}

// PresentBytes is how much of the plaintext is on disk, for progress reporting.
func (s *SparseFile) PresentBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.presentCount == len(s.present) {
		return s.plaintextLen
	}
	return int64(s.presentCount) * ChunkSize
}

// Size is the full plaintext length.
func (s *SparseFile) Size() int64 { return s.plaintextLen }

// MissingFrom returns the first chunk at or after index that has not arrived,
// and false when everything from there on is present.
func (s *SparseFile) MissingFrom(index int) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := index; i < len(s.present); i++ {
		if !s.present[i] {
			return i, true
		}
	}
	return 0, false
}

// WaitFor blocks until chunk index is present, the file is closed, or the
// caller's done channel fires.
func (s *SparseFile) WaitFor(index int, done <-chan struct{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if index < 0 || index >= len(s.present) {
		return io.EOF
	}
	// sync.Cond cannot select on a channel, so a watcher goroutine broadcasts
	// on cancellation and the predicate below rechecks.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-done:
			s.mu.Lock()
			s.cond.Broadcast()
			s.mu.Unlock()
		case <-stop:
		}
	}()

	for !s.present[index] && !s.closed {
		select {
		case <-done:
			return context.Canceled
		default:
		}
		s.cond.Wait()
	}
	if s.closed {
		return os.ErrClosed
	}
	if !s.present[index] {
		return context.Canceled
	}
	return nil
}

// ReadAt reads plaintext that has already arrived. It never blocks and never
// returns bytes from chunks that are still missing; the caller waits first.
func (s *SparseFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.plaintextLen {
		return 0, io.EOF
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, os.ErrClosed
	}

	// Clamp the read to the run of chunks present from off, so a partially
	// filled file never hands out zeroes.
	limit := off
	for i := int(off / ChunkSize); i < len(s.present) && s.present[i]; i++ {
		_, end := ChunkRange(i, s.plaintextLen)
		limit = end
	}
	if limit <= off {
		return 0, errNotPresent
	}
	if int64(len(p)) > limit-off {
		p = p[:limit-off]
	}
	return s.file.ReadAt(p, off)
}

var errNotPresent = errors.New("mediastream: requested bytes have not arrived yet")

// Close releases the file. The data and index stay on disk for resume.
func (s *SparseFile) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cond.Broadcast()
	return s.file.Close()
}

// Discard closes the file and removes both it and its index, for a partial
// download that turned out to be unusable.
func (s *SparseFile) Discard(path string) {
	s.Close()
	os.Remove(path)
	os.Remove(s.indexPath)
}
