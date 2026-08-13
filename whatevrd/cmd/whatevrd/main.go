package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"whatevrd/internal/app"
	"whatevrd/internal/notify"
	"whatevrd/internal/protocol"
	"whatevrd/internal/store"
	"whatevrd/internal/wa"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	paths, err := app.ResolvePaths()
	if err != nil {
		log.Fatalf("resolve paths: %v", err)
	}

	if err := paths.Ensure(); err != nil {
		log.Fatalf("create runtime/data directories: %v", err)
	}

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
