package app

import (
	"errors"
	"os"
	"path/filepath"
)

type Paths struct {
	RuntimeDir string
	// SocketDir/SocketPath serve the whatevr protocol (PROTOCOL.md) — the
	// daemon's only socket.
	SocketDir  string
	SocketPath string
	// LockDir/LockPath deliberately keep the pre-teardown location rather than
	// moving into SocketDir: a pre-teardown daemon (which still binds the gRPC
	// socket and locks here) and this one must continue to exclude each other,
	// or an upgrade could leave two daemons on one SQLite database. Safe to
	// fold into SocketDir once no such build can still be running.
	LockDir       string
	LockPath      string
	DataDir       string
	CacheDir      string
	DatabasePath  string
	SessionDir    string
	SessionDBPath string
	MediaCacheDir string
}

func ResolvePaths() (Paths, error) {
	runtimeBase := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeBase == "" {
		return Paths{}, errors.New("XDG_RUNTIME_DIR is not set")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}

	dataBase := os.Getenv("XDG_DATA_HOME")
	if dataBase == "" {
		dataBase = filepath.Join(home, ".local", "share")
	}

	cacheBase := os.Getenv("XDG_CACHE_HOME")
	if cacheBase == "" {
		cacheBase = filepath.Join(home, ".cache")
	}

	socketDir := filepath.Join(runtimeBase, "whatevr")
	lockDir := filepath.Join(runtimeBase, "whatevrd")
	dataDir := filepath.Join(dataBase, "whatevrd")
	cacheDir := filepath.Join(cacheBase, "whatevrd")

	return Paths{
		RuntimeDir:    runtimeBase,
		SocketDir:     socketDir,
		SocketPath:    filepath.Join(socketDir, "whatevrd.sock"),
		LockDir:       lockDir,
		LockPath:      filepath.Join(lockDir, "whatevrd.lock"),
		DataDir:       dataDir,
		CacheDir:      cacheDir,
		DatabasePath:  filepath.Join(dataDir, "whatevrd.db"),
		SessionDir:    filepath.Join(dataDir, "session"),
		SessionDBPath: filepath.Join(dataDir, "session", "whatsmeow.db"),
		MediaCacheDir: filepath.Join(cacheDir, "media"),
	}, nil
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.SocketDir, p.LockDir, p.DataDir, p.SessionDir, p.CacheDir, p.MediaCacheDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	return nil
}
