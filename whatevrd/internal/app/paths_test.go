package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolvePathsSocketAndLockLocations pins the two runtime paths that the
// gRPC teardown moved apart: the daemon's only socket is the whatevr protocol
// socket PROTOCOL.md names, while the process lock deliberately stays where
// pre-teardown builds put it so the two still exclude each other.
func TestResolvePathsSocketAndLockLocations(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(runtimeDir, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(runtimeDir, "cache"))

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}

	if want := filepath.Join(runtimeDir, "whatevr", "whatevrd.sock"); paths.SocketPath != want {
		t.Errorf("SocketPath = %q, want %q", paths.SocketPath, want)
	}
	if want := filepath.Join(runtimeDir, "whatevrd", "whatevrd.lock"); paths.LockPath != want {
		t.Errorf("LockPath = %q, want %q", paths.LockPath, want)
	}

	if err := paths.Ensure(); err != nil {
		t.Fatalf("ensure directories: %v", err)
	}
	for _, dir := range []string{paths.SocketDir, paths.LockDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s permissions = %o, want 700", dir, perm)
		}
	}
}
