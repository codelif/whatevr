package protocol

import (
	"context"
	"strings"
)

type backupExportParams struct {
	Path       string `json:"path"`
	Passphrase string `json:"passphrase"`
	UseKeyring bool   `json:"use_keyring"`
}

// daemon.backup_export writes a backup bundle (message store + session +
// media) to path, defaulting to a timestamped file. A passphrase encrypts it
// (AES-256-GCM via scrypt); use_keyring reads that passphrase from the OS
// keyring instead of the wire. The socket is user-private (0700), so a
// wire passphrase is no more exposed than the messages themselves.
func (h commandHandlers) backupExport(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p backupExportParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	path, size, err := h.actions.ExportBackup(ctx, strings.TrimSpace(p.Path), p.Passphrase, p.UseKeyring)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"path": path, "size_bytes": size}, nil
}

type backupSetPassphraseParams struct {
	Passphrase string `json:"passphrase"`
}

// daemon.backup_set_passphrase stores the backup passphrase in the OS keyring
// (Secret Service), so later exports can pass use_keyring instead of the
// secret itself.
func (h commandHandlers) backupSetPassphrase(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p backupSetPassphraseParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Passphrase) == "" {
		return nil, errorf(CodeInvalidParams, "passphrase is required")
	}
	return nil, mapCommandError(h.actions.SetBackupPassphrase(ctx, p.Passphrase))
}

type daemonLogsParams struct {
	Limit int `json:"limit"`
}

// daemon.logs returns recent daemon log lines (oldest first) from the
// in-memory ring for debugging. The rotated files on disk hold more. Local
// and synchronous: nothing here touches the network.
func (h commandHandlers) daemonLogs(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p daemonLogsParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	lines, err := h.actions.RecentLogs(context.Background(), p.Limit)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	if lines == nil {
		lines = []string{}
	}
	return map[string]any{"lines": lines}, nil
}
