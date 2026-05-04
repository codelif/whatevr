package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"whatevrd/internal/app"
	"whatevrd/internal/notify"
	daemonrpc "whatevrd/internal/rpc"
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
	notificationWorker, err := notify.NewWorker()
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

	server, err := daemonrpc.Start(ctx, paths.SocketPath, daemon, waClient, waClient, db, waClient, waClient, waClient)
	if err != nil {
		log.Fatalf("start rpc server: %v", err)
	}
	waClient.Start(ctx)

	log.Printf("whatevrd listening on %s", paths.SocketPath)

	select {
	case <-ctx.Done():
		log.Print("whatevrd shutting down")
		if err := <-server.Err(); err != nil {
			log.Fatalf("rpc server failed during shutdown: %v", err)
		}
	case err := <-server.Err():
		if err != nil {
			log.Fatalf("rpc server failed: %v", err)
		}
	}
}
