package wa

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

type Client struct {
	daemon    *app.Daemon
	store     *appstore.DB
	container *sqlstore.Container
	paths     app.Paths
	log       waLog.Logger

	mu     sync.Mutex
	client *whatsmeow.Client

	presenceMu       sync.Mutex
	frontendSessions int
	lastPresence     types.Presence
}

func New(ctx context.Context, paths app.Paths, daemon *app.Daemon, store *appstore.DB) (*Client, error) {
	log := waLog.Stdout("whatevrd/wa", "WARN", false)
	container, err := openSessionStore(ctx, paths.SessionDBPath, log.Sub("DB"))
	if err != nil {
		return nil, err
	}

	c := &Client{
		daemon:    daemon,
		store:     store,
		container: container,
		paths:     paths,
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
	client.EnableAutoReconnect = true
	client.AutoTrustIdentity = true
	client.SetForceActiveDeliveryReceipts(true)
	client.UseRetryMessageStore = true
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

func (c *Client) FrontendSessionStarted() {
	c.presenceMu.Lock()
	c.frontendSessions++
	shouldSync := c.frontendSessions == 1
	c.presenceMu.Unlock()

	if shouldSync {
		c.syncPresence(context.Background(), true)
	}
}

func (c *Client) FrontendSessionEnded() {
	c.presenceMu.Lock()
	if c.frontendSessions > 0 {
		c.frontendSessions--
	}
	shouldSync := c.frontendSessions == 0
	c.presenceMu.Unlock()

	if shouldSync {
		c.syncPresence(context.Background(), true)
	}
}

func (c *Client) syncPresence(ctx context.Context, force bool) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}

	c.presenceMu.Lock()
	desired := types.PresenceUnavailable
	if c.frontendSessions > 0 {
		desired = types.PresenceAvailable
	}
	if !force && c.lastPresence == desired {
		c.presenceMu.Unlock()
		return
	}
	c.lastPresence = desired
	c.presenceMu.Unlock()

	if err := client.SendPresence(ctx, desired); err != nil {
		c.log.Warnf("Failed to update presence to %s: %v", desired, err)
	}
}

func (c *Client) migrateLIDChats(ctx context.Context) {
	client := c.currentClient()
	if client == nil || client.Store.LIDs == nil {
		return
	}

	lidChats, err := c.store.ListLIDChats(ctx)
	if err != nil {
		c.log.Warnf("Failed to list LID chats: %v", err)
		return
	}

	for _, lidChatID := range lidChats {
		lidJID, err := types.ParseJID(lidChatID)
		if err != nil {
			continue
		}

		pnJID, err := client.Store.LIDs.GetPNForLID(ctx, lidJID)
		if err != nil || pnJID.IsEmpty() {
			continue
		}

		chat, migrated, err := c.store.MigrateChatID(ctx, lidChatID, pnJID.String())
		if err != nil {
			c.log.Warnf("Failed to migrate LID chat %s -> %s: %v", lidChatID, pnJID, err)
			continue
		}

		if migrated {
			c.log.Infof("Migrated LID chat %s -> %s", lidChatID, pnJID)
			c.daemon.PublishChatMigrated(lidChatID, toDaemonChat(chat))
		}
	}
}

func sqliteDSN(path string) string {
	return fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", filepath.ToSlash(path))
}

func openSessionStore(ctx context.Context, path string, log waLog.Logger) (*sqlstore.Container, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}

	// whatsmeow writes session metadata from multiple event handlers. Keep SQLite
	// serialized to avoid SQLITE_BUSY during bursts such as push-name updates.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	container := sqlstore.NewWithDB(db, "sqlite", log)
	if err := container.Upgrade(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return container, nil
}
