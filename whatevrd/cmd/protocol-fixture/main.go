package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"whatevrd/internal/app"
	"whatevrd/internal/protocol"
)

type conformanceView struct{}

func (conformanceView) Open(_ json.RawMessage, _ func()) (protocol.ViewSession, map[string]any, *protocol.Error) {
	return conformanceSession{}, map[string]any{"fixture": "conformance"}, nil
}

type conformanceSession struct{}

func (conformanceSession) Items(max int) []protocol.Item {
	items := []protocol.Item{
		{ID: "alpha", Sort: "0001", Data: map[string]any{"id": "alpha", "title": "Alpha"}},
		{ID: "bravo", Sort: "0002", Data: map[string]any{"id": "bravo", "title": "Bravo"}},
		{ID: "charlie", Sort: "0003", Data: map[string]any{"id": "charlie", "title": "Charlie"}},
	}
	if max > 0 && len(items) > max {
		items = items[:max]
	}
	return append([]protocol.Item(nil), items...)
}

func (conformanceSession) Close() {}

func main() {
	var socketPath string
	var readyFile string
	flag.StringVar(&socketPath, "socket", "", "unix socket path to serve")
	flag.StringVar(&readyFile, "ready-file", "", "write this file after the fixture is listening")
	flag.Parse()

	if socketPath == "" {
		log.Fatal("--socket is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	base := filepath.Dir(socketPath)
	daemon := app.NewDaemon(app.Paths{
		DataDir:  filepath.Join(base, "data"),
		CacheDir: filepath.Join(base, "cache"),
	})
	daemon.SetState(app.StateOnline)

	server, err := protocol.Start(ctx, socketPath, daemon)
	if err != nil {
		log.Fatalf("start protocol fixture: %v", err)
	}
	protocol.RegisterDaemonViews(server, daemon, nil, nil)
	server.RegisterView("conformance", conformanceView{})

	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte(socketPath+"\n"), 0o644); err != nil {
			log.Fatalf("write ready file: %v", err)
		}
	}
	log.Printf("protocol conformance fixture listening on %s", socketPath)

	select {
	case <-ctx.Done():
		for err := range server.Err() {
			if err != nil {
				log.Fatalf("protocol fixture shutdown: %v", err)
			}
		}
	case err, ok := <-server.Err():
		if ok && err != nil {
			log.Fatalf("protocol fixture failed: %v", err)
		}
	}
}
