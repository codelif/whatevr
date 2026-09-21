package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"whatevrd/internal/app"
	"whatevrd/internal/backup"
	"whatevrd/internal/logfile"
	"whatevrd/internal/notify"
	"whatevrd/internal/protocol"
	"whatevrd/internal/store"
	"whatevrd/internal/tray"
	"whatevrd/internal/wa"
)

func main() {
	restoreBundle := flag.String("restore", "", "restore a backup bundle created by daemon.backup_export and exit (the daemon must be stopped)")
	restorePassphrase := flag.String("restore-passphrase", "", "passphrase for an encrypted backup bundle (prefer WHATEVR_BACKUP_PASSPHRASE)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	paths, err := app.ResolvePaths()
	if err != nil {
		log.Fatalf("resolve paths: %v", err)
	}

	if err := paths.Ensure(); err != nil {
		log.Fatalf("create runtime/data directories: %v", err)
	}

	// Debug log: stderr plus a size-rotated file in the cache dir. First
	// thing after the dirs exist, so every later log line is captured.
	logHandle := logfile.Init(paths.CacheDir)
	defer logHandle.Close()
	log.Printf("debug log: %s", logHandle.Path())

	// Adopt a systemd-activated socket if present (and clear LISTEN_* so it is
	// never inherited by child processes). nil means run standalone.
	activatedListener, err := app.SystemdListener()
	if err != nil {
		log.Fatalf("adopt systemd socket: %v", err)
	}

	processLock, err := app.AcquireProcessLock(paths.LockPath)
	if err != nil {
		log.Fatalf("acquire process lock: %v", err)
	}
	defer processLock.Close()

	// Offline restore runs before anything opens the databases; holding the
	// process lock above is what guarantees no live daemon is writing while
	// the bundle lands. The lock refuses when one is running.
	if *restoreBundle != "" {
		passphrase := []byte(*restorePassphrase)
		if len(passphrase) == 0 {
			passphrase = []byte(os.Getenv("WHATEVR_BACKUP_PASSPHRASE"))
		}
		if err := backup.Restore(paths, *restoreBundle, passphrase); err != nil {
			log.Fatalf("restore: %v", err)
		}
		log.Printf("restored backup %s", *restoreBundle)
		return
	}

	db, err := store.Open(ctx, paths.DatabasePath)
	if err != nil {
		log.Fatalf("open sqlite database: %v", err)
	}
	defer db.Close()

	daemon := app.NewDaemon(paths)
	// The whatevr protocol server (PROTOCOL.md) is the daemon's only frontend
	// interface.
	protocolServer, err := protocol.New(paths.SocketPath, activatedListener, daemon)
	if err != nil {
		log.Fatalf("start protocol server: %v", err)
	}

	// The protocol server routes daemon→frontend pushes (open_chat on a
	// notification click) as connection-directed events.
	notificationWorker, err := notify.NewWorker(protocolServer)
	if err != nil {
		log.Printf("notifications disabled: %v", err)
	}
	if notificationWorker != nil {
		notificationWorker.Start(ctx)
	}

	// Daemon tray icon (StatusNotifierItem): connection state + unread count.
	// Best-effort — a missing session bus or watcher only logs.
	go tray.Start(ctx, daemon, db, protocolServer)

	waClient, err := wa.New(ctx, paths, daemon, db, notificationWorker)
	if err != nil {
		log.Fatalf("initialize WhatsApp client: %v", err)
	}
	defer waClient.Close()

	// The loopback range server backs media.stream: it hands players the bytes
	// of an in-progress download. It is bound before commands are registered
	// so a media.stream can never arrive before there is somewhere to point it.
	if err := waClient.StartMediaServer(); err != nil {
		log.Printf("media streaming disabled: %v", err)
	}
	defer waClient.StopMediaServer()

	protocol.RegisterDaemonViews(protocolServer, daemon, db, waClient)
	protocol.RegisterDaemonCommands(protocolServer, waClient)
	if protocol.DevCommandsEnabled() {
		// Not part of PROTOCOL.md, off unless the environment asks for it. See
		// internal/protocol/dev_commands.go.
		protocol.RegisterDevCommands(protocolServer, waClient)
		log.Printf("development commands enabled (%s=1)", protocol.DevEnvVar)
	}
	// Every view and command is registered above; only now do we accept
	// connections, so no client can race a half-populated handler surface.
	protocolServer.Serve(ctx)
	waClient.Start(ctx)

	log.Printf("whatevrd listening on %s", paths.SocketPath)

	select {
	case <-ctx.Done():
		log.Print("whatevrd shutting down")
		if err := <-protocolServer.Err(); err != nil {
			log.Fatalf("protocol server failed during shutdown: %v", err)
		}
	case err := <-protocolServer.Err():
		if err != nil {
			log.Fatalf("protocol server failed: %v", err)
		}
	}
}
