package wa

import (
	"context"
	"errors"
	"strings"

	"whatevrd/internal/app"
	"whatevrd/internal/backup"
	"whatevrd/internal/logfile"
)

// ExportBackup writes an encrypted-or-plain backup bundle (message store +
// session + media) to dest, defaulting to a timestamped file next to the data
// dir. The passphrase comes from the argument, or from the OS keyring when
// useKeyring is set.
func (c *Client) ExportBackup(ctx context.Context, dest, passphrase string, useKeyring bool) (string, int64, error) {
	pass := []byte(passphrase)
	if len(pass) == 0 && useKeyring {
		keyringPass, err := backup.GetBackupPassphrase()
		if err != nil {
			return "", 0, err
		}
		pass = keyringPass
	}
	if strings.TrimSpace(dest) == "" {
		dest = backup.DefaultDest(c.paths)
	}
	size, err := backup.Export(ctx, c.paths, dest, pass)
	if err != nil {
		return "", 0, err
	}
	return dest, size, nil
}

// RecentLogs serves the daemon's in-memory log ring for the `daemon.logs`
// query. Limit is clamped to the ring size; the file on disk always holds
// more (5 MiB × 3 rotated files).
func (c *Client) RecentLogs(ctx context.Context, limit int) ([]string, error) {
	return logfile.TailDefault(limit), nil
}

// SetBackupPassphrase stores the backup passphrase in the OS keyring
// (Secret Service: GNOME Keyring, KWallet, KeePassXC…).
func (c *Client) SetBackupPassphrase(ctx context.Context, passphrase string) error {
	if strings.TrimSpace(passphrase) == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "passphrase is required")
	}
	if err := backup.SetBackupPassphrase([]byte(passphrase)); err != nil {
		if errors.Is(err, backup.ErrNoKeyring) {
			return app.NewCommandError(app.CommandErrorRejected, "no secret service on the session bus: pass the passphrase explicitly")
		}
		return err
	}
	return nil
}
