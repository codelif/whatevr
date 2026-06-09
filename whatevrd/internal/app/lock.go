package app

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var ErrAlreadyRunning = errors.New("another whatevrd process is already running")

type ProcessLock struct {
	file *os.File
	path string
}

func AcquireProcessLock(path string) (*ProcessLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyRunning, path)
		}
		return nil, err
	}

	return &ProcessLock{file: file, path: path}, nil
}

func (l *ProcessLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}

	// Unlink the lock file before releasing the lock so a concurrent acquirer
	// never observes a stale, unheld file. We still hold the flock here, so the
	// path we created is the one being removed.
	if l.path != "" {
		if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			// Best-effort: fall through to unlock/close so the fd is released.
			_ = err
		}
	}

	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
