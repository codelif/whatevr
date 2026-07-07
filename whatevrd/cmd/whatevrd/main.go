package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"whatevrd/internal/app"
	"whatevrd/internal/notify"
	"whatevrd/internal/protocol"
	daemonrpc "whatevrd/internal/rpc"
	"whatevrd/internal/store"
	"whatevrd/internal/wa"
)

type multiChatOpener []notify.ChatOpener

func (m multiChatOpener) OpenChat(chatID string) bool {
	delivered := false
	for _, opener := range m {
		if opener != nil && opener.OpenChat(chatID) {
			delivered = true
		}
	}
	return delivered
}

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
	// The whatevr protocol server (PROTOCOL.md) runs alongside gRPC for the
	// duration of the migration; frontends move over view by view.
	protocolServer, err := protocol.Start(ctx, paths.ProtocolSocketPath, daemon)
	if err != nil {
		log.Fatalf("start protocol server: %v", err)
	}

	// The session bus carries daemon→frontend pushes (e.g. open-chat on
	// notification click) for the legacy gRPC HoldSession streams. The protocol
	// server implements the same opener for connection-directed open_chat events;
	// the notification worker fans to both during the migration.
	sessionBus := daemonrpc.NewSessionBus()
	notificationWorker, err := notify.NewWorker(multiChatOpener{sessionBus, protocolServer})
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

	server, err := daemonrpc.Start(ctx, paths.SocketPath, activatedListener, daemon, waClient, waClient, sessionBus, db, waClient, waClient, waClient, waClient, waClient)
	if err != nil {
		log.Fatalf("start rpc server: %v", err)
	}

	protocol.RegisterDaemonViews(protocolServer, daemon, db, waClient)
	protocol.RegisterDaemonCommands(protocolServer, waClient)
	waClient.Start(ctx)

	log.Printf("whatevrd listening on %s (grpc) and %s (protocol)", paths.SocketPath, paths.ProtocolSocketPath)

	select {
	case <-ctx.Done():
		log.Print("whatevrd shutting down")
		if err := <-server.Err(); err != nil {
			log.Fatalf("rpc server failed during shutdown: %v", err)
		}
		if err := <-protocolServer.Err(); err != nil {
			log.Fatalf("protocol server failed during shutdown: %v", err)
		}
	case err := <-server.Err():
		if err != nil {
			log.Fatalf("rpc server failed: %v", err)
		}
	case err := <-protocolServer.Err():
		if err != nil {
			log.Fatalf("protocol server failed: %v", err)
		}
	}
}
