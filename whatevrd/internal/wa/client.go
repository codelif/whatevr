package wa

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatevrd/internal/app"
)

type Client struct {
	daemon    *app.Daemon
	container *sqlstore.Container
	log       waLog.Logger

	mu     sync.Mutex
	client *whatsmeow.Client
}

func New(ctx context.Context, paths app.Paths, daemon *app.Daemon) (*Client, error) {
	log := waLog.Stdout("whatevrd/wa", "WARN", false)
	container, err := sqlstore.New(ctx, "sqlite", sqliteDSN(paths.SessionDBPath), log.Sub("DB"))
	if err != nil {
		return nil, err
	}

	c := &Client{
		daemon:    daemon,
		container: container,
		log:       log,
	}

	if err := c.resetClient(ctx); err != nil {
		container.Close()
		return nil, err
	}

	return c, nil
}

func (c *Client) Start(ctx context.Context) {
	go c.start(ctx)
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.client != nil {
		c.client.Disconnect()
	}
	c.mu.Unlock()

	return c.container.Close()
}

func (c *Client) resetClient(ctx context.Context) error {
	device, err := c.container.GetFirstDevice(ctx)
	if err != nil {
		return err
	}

	client := whatsmeow.NewClient(device, c.log.Sub("Client"))
	client.BackgroundEventCtx = ctx
	client.AddEventHandler(c.handleEvent)

	c.mu.Lock()
	c.client = client
	c.mu.Unlock()

	return nil
}

func (c *Client) currentClient() *whatsmeow.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func sqliteDSN(path string) string {
	return fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", filepath.ToSlash(path))
}
