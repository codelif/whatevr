package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"whatevrd/internal/app"
	daemonrpc "whatevrd/internal/rpc"
	"whatevrd/internal/store"
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

	db, err := store.Open(ctx, paths.DatabasePath)
	if err != nil {
		log.Fatalf("open sqlite database: %v", err)
	}
	defer db.Close()

	daemon := app.NewDaemon(paths)
	daemon.SetState(app.StateNeedLogin)

	server, err := daemonrpc.Start(ctx, paths.SocketPath, daemon)
	if err != nil {
		log.Fatalf("start rpc server: %v", err)
	}

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
