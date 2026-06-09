package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireProcessLockPreventsSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatevrd.lock")

	first, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer first.Close()

	second, err := AcquireProcessLock(path)
	if err == nil {
		second.Close()
		t.Fatal("expected second lock acquisition to fail")
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}
}

func TestAcquireProcessLockReleasesOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatevrd.lock")

	first, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first lock: %v", err)
	}

	second, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatalf("acquire second lock after close: %v", err)
	}
	defer second.Close()
}

func TestProcessLockRemovesFileOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatevrd.lock")

	lock, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected lock file to exist while held: %v", err)
	}

	if err := lock.Close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock file to be removed after close, stat err: %v", err)
	}
}
