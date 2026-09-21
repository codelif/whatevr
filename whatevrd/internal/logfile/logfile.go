package logfile

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const (
	logFileName = "whatevrd.log"
	maxBytes    = 5 * 1024 * 1024
	keepFiles   = 3
	maxLines    = 1000
)

// MaxLines is the ring capacity: the most lines any Tail can serve. Exported
// for callers sizing subscriptions over the tail.
const MaxLines = maxLines

var (
	defaultMu     sync.Mutex
	defaultHandle *Handle
)

// Handle is the daemon's log sink: stderr plus a size-rotated file plus an
// in-memory ring of recent lines for the `daemon.logs` query. Use Init once
// at startup; Tail serves reads from anywhere.
type Handle struct {
	file *rotatingFile

	mu    sync.Mutex
	lines [][]byte
}

// Init opens the log file under dir, points the standard logger at
// stderr+file, and returns the handle. A missing dir or an unopenable file
// only logs to stderr: debugging must never prevent startup. Init also
// installs the handle as the process default for TailDefault.
func Init(dir string) *Handle {
	h := &Handle{}
	writers := []io.Writer{os.Stderr}
	if dir != "" {
		if rf, err := openRotating(dir); err != nil {
			fmt.Fprintf(os.Stderr, "log file disabled: %v\n", err)
		} else {
			h.file = rf
			writers = append(writers, h)
		}
	}
	log.SetOutput(io.MultiWriter(writers...))
	defaultMu.Lock()
	defaultHandle = h
	defaultMu.Unlock()
	return h
}

// Write implements io.Writer: every line lands in the ring, the whole chunk
// in the file.
func (h *Handle) Write(p []byte) (int, error) {
	h.mu.Lock()
	for _, line := range bytes.Split(p, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		h.lines = append(h.lines, cp)
		if overflow := len(h.lines) - maxLines; overflow > 0 {
			h.lines = append([][]byte(nil), h.lines[overflow:]...)
		}
	}
	h.mu.Unlock()
	if h.file == nil {
		return len(p), nil
	}
	return h.file.Write(p)
}

// Tail returns up to the last n logged lines, oldest first.
func (h *Handle) Tail(n int) []string {
	if n <= 0 {
		n = 200
	}
	if n > maxLines {
		n = maxLines
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n > len(h.lines) {
		n = len(h.lines)
	}
	out := make([]string, 0, n)
	for _, line := range h.lines[len(h.lines)-n:] {
		out = append(out, string(line))
	}
	return out
}

// TailDefault serves the process-default handle's ring (empty when Init
// never ran, e.g. in unit tests that log through their own handle).
func TailDefault(n int) []string {
	defaultMu.Lock()
	h := defaultHandle
	defaultMu.Unlock()
	if h == nil {
		return nil
	}
	return h.Tail(n)
}

// Close flushes and closes the log file.
func (h *Handle) Close() error {
	if h.file == nil {
		return nil
	}
	return h.file.Close()
}

// Path returns the active log file path, for "show in folder" affordances.
func (h *Handle) Path() string {
	if h.file == nil {
		return ""
	}
	return h.file.path
}

type rotatingFile struct {
	mu      sync.Mutex
	path    string
	written int64
	file    *os.File
}

func openRotating(dir string) (*rotatingFile, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, logFileName)
	var written int64
	if info, err := os.Stat(path); err == nil {
		written = info.Size()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	rf := &rotatingFile{path: path, written: written, file: f}
	if written >= maxBytes {
		if err := rf.rotateLocked(); err != nil {
			f.Close()
			return nil, err
		}
	}
	return rf, nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.written+int64(len(p)) > maxBytes {
		if err := r.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.written += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}

// rotateLocked closes the current file and shifts whatevrd.log → .1 → .2,
// dropping the oldest. The caller holds r.mu.
func (r *rotatingFile) rotateLocked() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	os.Remove(numbered(r.path, keepFiles))
	for i := keepFiles - 1; i >= 1; i-- {
		_ = os.Rename(numbered(r.path, i), numbered(r.path, i+1))
	}
	_ = os.Rename(r.path, numbered(r.path, 1))
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	r.file = f
	r.written = 0
	return nil
}

func numbered(path string, n int) string {
	return fmt.Sprintf("%s.%d", path, n)
}
