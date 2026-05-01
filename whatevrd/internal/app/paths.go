package app

import (
	"errors"
	"os"
	"path/filepath"
)

type Paths struct {
	RuntimeDir    string
	SocketDir     string
	SocketPath    string
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

	socketDir := filepath.Join(runtimeBase, "whatevrd")
	dataDir := filepath.Join(dataBase, "whatevrd")
	cacheDir := filepath.Join(cacheBase, "whatevrd")

	return Paths{
		RuntimeDir:    runtimeBase,
		SocketDir:     socketDir,
		SocketPath:    filepath.Join(socketDir, "whatevrd.sock"),
		DataDir:       dataDir,
		CacheDir:      cacheDir,
		DatabasePath:  filepath.Join(dataDir, "whatevrd.db"),
		SessionDir:    filepath.Join(dataDir, "session"),
		SessionDBPath: filepath.Join(dataDir, "session", "whatsmeow.db"),
		MediaCacheDir: filepath.Join(cacheDir, "media"),
	}, nil
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.SocketDir, p.DataDir, p.SessionDir, p.CacheDir, p.MediaCacheDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	return nil
}
