package wa

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
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
	"whatevrd/internal/notify"
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

	// pendingAppState parks pin/archive/mute state for chats whose JID has no
	// canonical chat yet (a LID without a PN mapping and without a chat row).
	// Entries are re-applied by reconcilePendingAppState as LID→PN mappings
	// land with history sync chunks; see pins.go.
	pendingAppStateMu sync.Mutex
	pendingAppState   map[types.JID]pendingAppStateEntry

	lifecycleMu sync.Mutex
	runMu       sync.Mutex
	runCtx      context.Context
	runCancel   context.CancelFunc
	runWG       sync.WaitGroup

	presenceMu       sync.Mutex
	frontendSessions map[string]frontendSession
	lastPresence     types.Presence
	// presenceOfflineTimer delays the available->unavailable transition so a
	// brief focus loss doesn't flip the account to "last seen". The generation
	// counter invalidates a fired callback that lost a cancel race for the lock.
	presenceOfflineTimer *time.Timer
	presenceTimerGen     uint64

	// Two-priority avatar fetch queue (see avatars.go): visible subjects
	// drain first, background ones pass through a token bucket.
	avatarMu          sync.Mutex
	avatarHigh        []avatarJob
	avatarLow         []avatarJob
	avatarQueued      map[appstore.AvatarSubject]avatarPriority
	avatarWake        chan struct{}
	avatarRefreshKick chan struct{}
	avatarTokenMu     sync.Mutex
	avatarTokens      float64
	avatarTokensAt    time.Time

	// Group member lists back the all-members receipt aggregation. The
	// freshness map throttles GetGroupInfo fetches: a stale group is
	// refreshed once in the background while receipts keep flowing.
	groupParticipantsMu       sync.Mutex
	groupParticipantsFresh    map[string]time.Time
	groupParticipantsInFlight map[string]bool

	mediaDownloadMu sync.Mutex
	mediaDownloads  map[string]*mediaDownloadState
	mediaRetryMu    sync.Mutex
	mediaRetries    map[string]*mediaRetryState

	// In-progress ranged fetches, keyed by message ID, plus the loopback
	// server that serves them to players while they fill; see media_stream.go.
	mediaStreamMu    sync.Mutex
	mediaStreams     map[string]*mediaStreamEntry
	mediaStreamHTTP  *http.Client
	mediaServerMu    sync.Mutex
	mediaServer      *http.Server
	mediaServerAddr  string
	mediaServerToken string

	// One ffmpeg worker handles derived posters. New downloads sit ahead of
	// startup backfill work, and the map prevents duplicate queued decodes.
	posterMu        sync.Mutex
	posterHigh      []appstore.Message
	posterLow       []appstore.Message
	posterQueued    map[string]posterPriority
	posterWake      chan struct{}
	posterExtractor func(context.Context, string, string) error

	stickerMu            sync.Mutex
	stickerDownloads     map[string]*stickerFileDownloadState
	stickerDownloadSem   chan struct{}
	stickerLibraryTimers map[app.StickerSource]*time.Timer
	stickerHTTPClient    *http.Client
	// Serializes sticker store index refreshes (cheap, but no point racing).
	stickerIndexMu sync.Mutex

	historySyncMu      sync.Mutex
	historySyncRunning bool
	historySyncWake    bool
	// Stall watchdog: armed while a message-carrying initial sync is below
	// 100%. When the phone goes quiet past the timeout, the settle work runs
	// early and a STALLED phase event is published; see history_sync.go.
	historySyncStallTimer   *time.Timer
	historySyncLastActivity time.Time
	historySyncLastEvent    app.HistorySyncEvent

	// In-flight on-demand history requests, keyed by chat ID; see backfill.go.
	backfillMu       sync.Mutex
	backfillInFlight map[string]*backfillRequest

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
	sendTimingsMu sync.Mutex
	sendTimings   map[string]*sendTiming
	reconnectCh   chan struct{} // supervisor wakeup: reconnect immediately
	reconnectNow  atomic.Bool
	eventGen      atomic.Uint64
	pinBackfill   atomic.Bool

	// appPrefs caches the daemon_config user preferences so the hot notify and
	// media-ingestion gates never hit sqlite per message. Loaded at New() and
	// replaced on SetAppPreferences. appPrefsMu serializes read-modify-write
	// mutations (UpdateAppPreferences) so a partial patch cannot lose a
	// concurrent update.
	appPrefsMu sync.Mutex
	appPrefs   atomic.Pointer[app.AppPreferences]
}

type frontendSession struct {
	focused      bool
	activeChatID string
}

type MessageNotifier interface {
	NotifyMessage(context.Context, app.Message, app.Chat, notify.Options)
}

type mediaDownloadState struct {
	done    chan struct{}
	message appstore.Message
	err     error
	// cancel aborts this fetch. A download runs on a detached context so it
	// outlives the command that started it, which means the only way to stop a
	// 400 MiB video is to hold on to its cancel here.
	cancel context.CancelFunc
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
		posterQueued:     make(map[string]posterPriority),
		posterWake:       make(chan struct{}, 1),
		sendQueueWake:    make(chan struct{}, 1),
		reconnectCh:      make(chan struct{}, 1),
		stickerDownloads: make(map[string]*stickerFileDownloadState),
		sendTimings:      make(map[string]*sendTiming),
	}
	c.stickerDownloadSem = make(chan struct{}, stickerDownloadConcurrency)
	c.loadAppPreferences(ctx)

	storeLog := log.Sub("Store")
	store.SetSlowOpLogger(func(op string, d time.Duration) {
		storeLog.Warnf("Slow db op %s took %s", op, d.Round(time.Millisecond))
	})

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
	c.startRunGoroutine(func() { c.runVideoPosterWorker(runCtx) })
	c.startRunGoroutine(c.repairCachedWebPAlphaFlags)
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
	// Layer the daemon message store under whatsmeow's retry buffer so retry
	// receipts can be answered even after the buffer's 7-day retention.
	if device.EventBuffer != nil {
		device.EventBuffer = &retryFallbackBuffer{EventBuffer: device.EventBuffer, client: c}
	}

	client := whatsmeow.NewClient(device, c.log.Sub("Client"))
	client.BackgroundEventCtx = ctx
	client.EnableAutoReconnect = false
	client.ManualHistorySyncDownload = true
	client.DisableManualHistorySyncReceipt = true
	client.AutoTrustIdentity = autoTrustIdentityEnabled()
	if !client.AutoTrustIdentity {
		c.log.Warnf("WHATEVRD_AUTO_TRUST_IDENTITY is disabled; contacts whose WhatsApp identity changes (reinstall/re-register) stay unreachable in both directions until their stored identity is cleared manually")
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

// autoTrustIdentityEnabled reports whether changed WhatsApp identities are
// trusted automatically — the official-client behavior and the default: a
// contact who reinstalls WhatsApp keeps working, and the identity change is
// surfaced via DaemonEventIdentityChanged instead of a silent two-way decrypt
// deadlock. WHATEVRD_AUTO_TRUST_IDENTITY=0/false/no/off opts into strict mode.
func autoTrustIdentityEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WHATEVRD_AUTO_TRUST_IDENTITY"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
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

// presenceOfflineDelay is how long the account stays "available" after the last
// focused session goes away, so brief focus losses don't surface as "last seen".
// Variable so tests can shorten it.
var presenceOfflineDelay = 30 * time.Second

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
	if !force && desired == types.PresenceUnavailable && c.lastPresence != types.PresenceUnavailable {
		// Debounce available -> unavailable; forced syncs (session end,
		// reconnect) stay immediate.
		if c.presenceOfflineTimer == nil {
			c.presenceTimerGen++
			gen := c.presenceTimerGen
			c.presenceOfflineTimer = time.AfterFunc(presenceOfflineDelay, func() {
				c.presenceOfflineTimerFired(gen)
			})
		}
		c.presenceMu.Unlock()
		return
	}
	c.cancelPresenceOfflineTimerLocked()
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

// cancelPresenceOfflineTimerLocked stops any pending delayed offline transition.
// Caller must hold presenceMu.
func (c *Client) cancelPresenceOfflineTimerLocked() {
	c.presenceTimerGen++
	if c.presenceOfflineTimer != nil {
		c.presenceOfflineTimer.Stop()
		c.presenceOfflineTimer = nil
	}
}

func (c *Client) presenceOfflineTimerFired(gen uint64) {
	client := c.currentClient()
	c.presenceMu.Lock()
	if gen != c.presenceTimerGen {
		c.presenceMu.Unlock()
		return
	}
	c.presenceOfflineTimer = nil
	desired := desiredPresenceForSessions(c.frontendSessions)
	if desired != types.PresenceUnavailable || c.lastPresence == types.PresenceUnavailable {
		c.presenceMu.Unlock()
		return
	}
	if client == nil || !client.IsLoggedIn() {
		c.presenceMu.Unlock()
		return
	}
	c.lastPresence = types.PresenceUnavailable
	c.presenceMu.Unlock()

	if err := client.SendPresence(context.Background(), types.PresenceUnavailable); err != nil {
		c.log.Warnf("Failed to update presence to %s: %v", types.PresenceUnavailable, err)
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

	// Fresh mappings may also unblock app-state entries parked on LID JIDs.
	c.reconcilePendingAppState(ctx, false)
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
