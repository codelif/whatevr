package wa

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	notifier  MessageNotifier
	container *sqlstore.Container
	paths     app.Paths
	log       waLog.Logger

	mu     sync.Mutex
	client *whatsmeow.Client
	// WhatsApp app-state writes are version/hash based. Serialize our sends and
	// full syncs so rapid pin toggles don't encode against stale state.
	appStateMu sync.Mutex

	lifecycleMu sync.Mutex
	runMu       sync.Mutex
	runCtx      context.Context
	runCancel   context.CancelFunc
	runWG       sync.WaitGroup

	presenceMu       sync.Mutex
	frontendSessions map[string]frontendSession
	lastPresence     types.Presence

	avatarMu       sync.Mutex
	avatarQueue    chan avatarJob
	avatarQueued   map[appstore.AvatarSubject]bool
	avatarDeferred map[appstore.AvatarSubject]avatarJob

	mediaDownloadMu sync.Mutex
	mediaDownloads  map[string]*mediaDownloadState
	mediaRetryMu    sync.Mutex
	mediaRetries    map[string]*mediaRetryState

	historySyncMu             sync.Mutex
	historySyncRunning        bool
	historySyncWake           bool
	historySyncActive         bool
	historySyncLastActivity   time.Time
	historySyncIdleTimer      *time.Timer
	profilePictureSyncActive  bool
	profilePictureSyncRunning bool

	offlineSyncMu                sync.Mutex
	offlineSyncActive            bool
	offlineSyncTotalEvents       uint32
	offlineSyncTotalMessages     uint32
	offlineSyncProcessedEvents   uint32
	offlineSyncProcessedMessages uint32
	offlineSyncChangedChats      map[string]uint32
	offlineSyncLastPublish       time.Time

	sendQueueMu   sync.Mutex
	sendQueueWake chan struct{}
	reconnectCh   chan struct{} // supervisor wakeup: reconnect immediately
	reconnectNow  atomic.Bool
	eventGen      atomic.Uint64
	pinBackfill   atomic.Bool
}

type frontendSession struct {
	focused      bool
	activeChatID string
}

type MessageNotifier interface {
	NotifyMessage(context.Context, app.Message, app.Chat)
}

type mediaDownloadState struct {
	done    chan struct{}
	message appstore.Message
	err     error
}

type mediaRetryState struct {
	done       chan struct{}
	mediaKey   []byte
	directPath string
	err        error
	completed  bool
}

func New(ctx context.Context, paths app.Paths, daemon *app.Daemon, store *appstore.DB, notifier MessageNotifier) (*Client, error) {
	level := os.Getenv("WHATEVRD_LOG_LEVEL")
	if level == "" {
		level = "WARN"
	}
	log := waLog.Stdout("whatevrd/wa", level, false)
	container, err := openSessionStore(ctx, paths.SessionDBPath, log.Sub("DB"))
	if err != nil {
		return nil, err
	}

	c := &Client{
		daemon:           daemon,
		store:            store,
		container:        container,
		paths:            paths,
		notifier:         notifier,
		log:              log,
		frontendSessions: make(map[string]frontendSession),
		mediaDownloads:   make(map[string]*mediaDownloadState),
		mediaRetries:     make(map[string]*mediaRetryState),
		sendQueueWake:    make(chan struct{}, 1),
		reconnectCh:      make(chan struct{}, 1),
	}

	if err := c.resetClient(ctx); err != nil {
		container.Close()
		return nil, err
	}

	return c, nil
}

func (c *Client) Start(ctx context.Context) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	runCtx := c.replaceRunContextLocked(ctx)
	c.startRunGoroutine(func() { c.runConnectionSupervisor(runCtx) })
	c.startRunGoroutine(func() { c.runSendQueue(runCtx) })
	c.startAvatarWorker(runCtx)
}

func (c *Client) Reconnect(ctx context.Context) error {
	c.requestReconnect(true)
	return nil
}

func (c *Client) requestReconnect(forceClose bool) {
	if forceClose {
		c.reconnectNow.Store(true)
	}
	select {
	case c.reconnectCh <- struct{}{}:
	default:
	}
}

func (c *Client) Close() error {
	c.lifecycleMu.Lock()
	c.cancelRunContextLocked()
	c.lifecycleMu.Unlock()

	c.mu.Lock()
	if c.client != nil {
		c.client.Disconnect()
	}
	container := c.container
	c.mu.Unlock()

	if container == nil {
		return nil
	}
	return container.Close()
}

func (c *Client) replaceRunContextLocked(parent context.Context) context.Context {
	c.cancelRunContextLocked()

	if parent == nil {
		parent = context.Background()
	}
	c.runMu.Lock()
	c.runCtx, c.runCancel = context.WithCancel(parent)
	runCtx := c.runCtx
	c.runMu.Unlock()
	return runCtx
}

func (c *Client) cancelRunContextLocked() {
	c.runMu.Lock()
	if c.runCancel != nil {
		c.runCancel()
		c.runCancel = nil
	}
	c.runCtx = nil
	c.runMu.Unlock()
	c.runWG.Wait()
}

func (c *Client) startRunGoroutine(fn func()) {
	c.runWG.Add(1)
	go func() {
		defer c.runWG.Done()
		fn()
	}()
}

func (c *Client) cancelRunContext() {
	c.lifecycleMu.Lock()
	c.cancelRunContextLocked()
	c.lifecycleMu.Unlock()
}

func (c *Client) backgroundContext() context.Context {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if c.runCtx != nil {
		return c.runCtx
	}
	return context.Background()
}

func (c *Client) resetClient(ctx context.Context) error {
	device, err := c.container.GetFirstDevice(ctx)
	if err != nil {
		return err
	}

	client := whatsmeow.NewClient(device, c.log.Sub("Client"))
	client.BackgroundEventCtx = ctx
	client.EnableAutoReconnect = false
	client.ManualHistorySyncDownload = true
	client.DisableManualHistorySyncReceipt = true
	client.AutoTrustIdentity = autoTrustIdentityEnabled()
	if client.AutoTrustIdentity {
		c.log.Warnf("WHATEVRD_AUTO_TRUST_IDENTITY is enabled; changed WhatsApp identities will be trusted automatically")
	}
	client.SetForceActiveDeliveryReceipts(true)
	client.UseRetryMessageStore = true
	eventGen := c.eventGen.Add(1)
	client.AddEventHandler(func(raw any) {
		c.handleEvent(eventGen, raw)
	})

	c.mu.Lock()
	c.client = client
	c.mu.Unlock()

	return nil
}

func autoTrustIdentityEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WHATEVRD_AUTO_TRUST_IDENTITY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (c *Client) currentClient() *whatsmeow.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func (c *Client) FrontendSessionStarted(sessionID string) {
	if sessionID == "" {
		return
	}
	c.presenceMu.Lock()
	_, existed := c.frontendSessions[sessionID]
	c.frontendSessions[sessionID] = frontendSession{}
	shouldSync := !existed && len(c.frontendSessions) == 1
	c.presenceMu.Unlock()

	if shouldSync {
		c.syncPresence(context.Background(), true)
	}
}

func (c *Client) FrontendSessionEnded(sessionID string) {
	if sessionID == "" {
		return
	}
	c.presenceMu.Lock()
	_, existed := c.frontendSessions[sessionID]
	delete(c.frontendSessions, sessionID)
	shouldSync := existed && len(c.frontendSessions) == 0
	c.presenceMu.Unlock()

	if shouldSync {
		c.syncPresence(context.Background(), true)
	}
}

func (c *Client) FrontendSessionStateChanged(sessionID string, focused bool, activeChatID string) {
	if sessionID == "" {
		return
	}
	c.presenceMu.Lock()
	previous := c.frontendSessions[sessionID]
	c.frontendSessions[sessionID] = frontendSession{focused: focused, activeChatID: activeChatID}
	c.presenceMu.Unlock()

	if previous.focused != focused {
		c.syncPresence(context.Background(), false)
	}

}

func (c *Client) ShouldNotifyChat(chatID string) bool {
	c.presenceMu.Lock()
	defer c.presenceMu.Unlock()
	for _, session := range c.frontendSessions {
		if session.focused && session.activeChatID == chatID {
			return false
		}
	}
	return true
}

func (c *Client) syncPresence(ctx context.Context, force bool) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}

	c.presenceMu.Lock()
	desired := desiredPresenceForSessions(c.frontendSessions)
	if client.Store.PushName == "" && client.MessengerConfig == nil {
		c.presenceMu.Unlock()
		return
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

func desiredPresenceForSessions(sessions map[string]frontendSession) types.Presence {
	for _, session := range sessions {
		if session.focused {
			return types.PresenceAvailable
		}
	}
	return types.PresenceUnavailable
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
	return fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(path))
}

func openSessionStore(ctx context.Context, path string, log waLog.Logger) (*sqlstore.Container, error) {
	db, err := sql.Open(appstore.SQLiteDriverName, sqliteDSN(path))
	if err != nil {
		return nil, err
	}

	// whatsmeow writes session metadata from multiple event handlers. Keep SQLite
	// serialized to avoid SQLITE_BUSY during bursts such as push-name updates.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	container := sqlstore.NewWithDB(db, "sqlite3", log)
	if err := container.Upgrade(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return container, nil
}
