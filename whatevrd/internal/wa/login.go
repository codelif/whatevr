package wa

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"whatevrd/internal/app"
)

const (
	connBackoffBase = 5 * time.Second
	connBackoffMax  = 60 * time.Second
	qrRetryDelay    = 300 * time.Millisecond
)

var errQRLoginRetry = errors.New("retry QR login")

// runConnectionSupervisor replaces the old one-shot start(). It loops
// forever (until ctx is cancelled), connecting and retrying with
// exponential backoff on any failure.
func (c *Client) runConnectionSupervisor(ctx context.Context) {
	attempt := 0

	for {
		if ctx.Err() != nil {
			return
		}

		// Drain any stale reconnect signal before each attempt.
		select {
		case <-c.reconnectCh:
		default:
		}
		c.closeForReconnectIfRequested()

		err := c.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			retry := connectionRetry(attempt, err)
			attempt = retry.attempt
			if retry.nextRetryUnix {
				nextRetry := time.Now().Add(retry.delay)
				c.daemon.SetConnMeta(int32(attempt), nextRetry.Unix(), retry.canReconnect)
			} else {
				c.daemon.SetConnMeta(int32(attempt), 0, retry.canReconnect)
			}
			if retry.detail != "" {
				c.daemon.SetStateDetail(retry.state, retry.detail)
			}

			select {
			case <-ctx.Done():
				return
			case <-c.reconnectCh:
				attempt = 0
			case <-time.After(retry.delay):
			}
			continue
		}

		// Successful connect — reset backoff, clear retry meta.
		attempt = 0
		c.daemon.SetConnMeta(0, 0, false)

		// Park until a reconnect is signalled.
		if !c.waitForReconnectSignal(ctx) {
			return
		}

		// Brief pause so whatsmeow can finish cleanup.
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (c *Client) waitForReconnectSignal(ctx context.Context) bool {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-c.reconnectCh:
			return true
		case <-ticker.C:
			if c.connectionLooksOffline(ctx) {
				c.daemon.SetConnMeta(0, 0, true)
				c.daemon.SetStateDetail(app.StateOffline, "Connection lost. Reconnecting...")
				c.reconnectNow.Store(true)
				return true
			}
		}
	}
}

func (c *Client) closeForReconnectIfRequested() {
	if !c.reconnectNow.Swap(false) {
		return
	}
	if client := c.currentClient(); client != nil {
		client.Disconnect()
	}
}

func (c *Client) connectionLooksOffline(ctx context.Context) bool {
	client := c.currentClient()
	if client == nil || client.Store.ID == nil {
		return false
	}
	if !client.IsLoggedIn() || !client.IsConnected() {
		return true
	}
	if client.Store.PushName == "" && client.MessengerConfig == nil {
		return false
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.SendPresence(probeCtx, c.desiredPresence()); err != nil {
		c.log.Warnf("WhatsApp connection probe failed: %v", err)
		return true
	}
	return false
}

func (c *Client) desiredPresence() types.Presence {
	c.presenceMu.Lock()
	defer c.presenceMu.Unlock()
	if len(c.frontendSessions) > 0 {
		return types.PresenceAvailable
	}
	return types.PresenceUnavailable
}

// connectOnce performs a single connection attempt. For QR-login sessions it
// blocks until the QR flow concludes (success or failure). Returns nil on a
// successful connect; the supervisor will then park until signalled.
func (c *Client) connectOnce(ctx context.Context) error {
	if latestVer, err := whatsmeow.GetLatestVersion(ctx, nil); err != nil {
		c.log.Warnf("Failed to fetch latest WhatsApp version: %v", err)
	} else {
		store.SetWAVersion(*latestVer)
	}

	client := c.currentClient()
	if client == nil {
		return fmt.Errorf("WhatsApp client is not initialized")
	}

	if client.Store.ID == nil {
		return c.startQRLogin(ctx, client)
	}

	c.daemon.SetStateDetail(app.StateConnecting, "Connecting to WhatsApp...")
	if err := client.ConnectContext(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if client.IsLoggedIn() && client.IsConnected() {
		c.daemon.SetConnMeta(0, 0, false)
		c.daemon.SetStateDetail(app.StateOnline, "Connected to WhatsApp")
	}
	return nil
}

func (c *Client) startQRLogin(ctx context.Context, client *whatsmeow.Client) error {
	c.daemon.SetStateDetail(app.StateNeedLogin, "Waiting for WhatsApp QR scan")

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("create QR channel: %w", err)
	}

	if err := client.ConnectContext(ctx); err != nil {
		return fmt.Errorf("connect for QR login: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case item, ok := <-qrChan:
			if !ok {
				return fmt.Errorf("QR channel closed unexpectedly: %w", errQRLoginRetry)
			}

			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				c.daemon.PublishQRCode(item.Code, time.Now().Add(item.Timeout))
			case whatsmeow.QRChannelSuccess.Event:
				c.daemon.SetStateDetail(app.StateConnecting, "QR scanned; completing login")
				return nil
			case whatsmeow.QRChannelTimeout.Event:
				c.daemon.SetStateDetail(app.StateNeedLogin, "QR login timed out; scan a new code")
				return fmt.Errorf("QR login timed out: %w", errQRLoginRetry)
			case whatsmeow.QRChannelClientOutdated.Event:
				c.daemon.SetConnMeta(0, 0, false)
				c.daemon.SetStateDetail(app.StateOffline, "WhatsApp client is outdated. Update whatevr/whatevrd.")
				return fmt.Errorf("client outdated")
			case whatsmeow.QRChannelScannedWithoutMultidevice.Event:
				c.daemon.SetStateDetail(app.StateNeedLogin, "Enable multi-device on your phone and scan again")
				return fmt.Errorf("multi-device not enabled: %w", errQRLoginRetry)
			case whatsmeow.QRChannelEventError:
				c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("QR login error: %v", item.Error))
				if item.Error != nil {
					return errors.Join(errQRLoginRetry, item.Error)
				}
				return errQRLoginRetry
			}
		}
	}
}

type retryPlan struct {
	attempt       int
	delay         time.Duration
	state         app.State
	detail        string
	canReconnect  bool
	nextRetryUnix bool
}

func connectionRetry(attempt int, err error) retryPlan {
	if errors.Is(err, errQRLoginRetry) {
		return retryPlan{
			attempt: 0,
			delay:   qrRetryDelay,
			state:   app.StateNeedLogin,
		}
	}

	attempt++
	delay := connBackoffDelay(attempt)
	return retryPlan{
		attempt:       attempt,
		delay:         delay,
		state:         app.StateOffline,
		detail:        retryDetail(attempt, delay),
		canReconnect:  true,
		nextRetryUnix: true,
	}
}

func (c *Client) resetAfterExternalLogout() {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.eventGen.Add(1)

	ctx := c.backgroundContext()
	c.cancelRunContextLocked()
	ctx = context.Background()

	c.mu.Lock()
	old := c.client
	c.mu.Unlock()

	if old != nil {
		old.Disconnect()
		if old.Store.ID != nil {
			if err := old.Store.Delete(ctx); err != nil {
				c.log.Warnf("Failed to delete device store after remote logout: %v", err)
			}
		}
	}

	if err := c.resetClient(ctx); err != nil {
		c.log.Errorf("Failed to reset client after remote logout: %v", err)
		return
	}
	runCtx := c.replaceRunContextLocked(context.Background())
	c.startRunGoroutine(func() { c.runConnectionSupervisor(runCtx) })
	c.startRunGoroutine(func() { c.runSendQueue(runCtx) })
}

func (c *Client) Logout(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	c.daemon.SetStateDetail(app.StateConnecting, "Logging out and clearing local session data")
	c.eventGen.Add(1)

	client := c.currentClient()
	if client != nil {
		if client.Store.ID != nil {
			logoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := client.Logout(logoutCtx); err != nil && err != store.ErrDeviceDeleted {
				c.log.Warnf("Remote WhatsApp logout failed; clearing local session anyway: %v", err)
			}
			cancel()
		} else {
			client.Disconnect()
		}
	}
	c.cancelRunContextLocked()

	localCtx := context.Background()
	backupPath := filepath.Join(c.paths.DataDir, fmt.Sprintf("whatevrd-before-logout-%d.db", time.Now().Unix()))
	if err := c.store.Backup(localCtx, backupPath); err != nil {
		c.log.Warnf("Failed to back up local database before logout: %v", err)
	} else {
		c.log.Infof("Backed up local database before logout to %s", backupPath)
	}

	if err := c.store.ClearSessionData(localCtx); err != nil {
		return err
	}

	if err := os.RemoveAll(c.paths.MediaCacheDir); err != nil {
		return err
	}
	if err := os.MkdirAll(c.paths.MediaCacheDir, 0o700); err != nil {
		return err
	}

	c.mu.Lock()
	if c.client != nil {
		c.client.Disconnect()
		c.client = nil
	}
	oldContainer := c.container
	c.container = nil
	c.mu.Unlock()

	if oldContainer != nil {
		if err := oldContainer.Close(); err != nil {
			return err
		}
	}

	if err := os.RemoveAll(c.paths.SessionDir); err != nil {
		return err
	}
	if err := os.MkdirAll(c.paths.SessionDir, 0o700); err != nil {
		return err
	}

	container, err := openSessionStore(localCtx, c.paths.SessionDBPath, c.log.Sub("DB"))
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.container = container
	c.mu.Unlock()

	if err := c.resetClient(localCtx); err != nil {
		return err
	}

	c.daemon.SetStateDetail(app.StateNeedLogin, "Logged out")
	runCtx := c.replaceRunContextLocked(context.Background())
	c.startRunGoroutine(func() { c.runConnectionSupervisor(runCtx) })
	c.startRunGoroutine(func() { c.runSendQueue(runCtx) })
	return nil
}

func connBackoffDelay(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 4 {
		shift = 4
	}
	delay := connBackoffBase * (1 << uint(shift))
	if delay > connBackoffMax {
		delay = connBackoffMax
	}
	// Add up to 20% jitter.
	jitter := time.Duration(rand.Int64N(int64(delay / 5)))
	return delay + jitter
}

func retryDetail(attempt int, delay time.Duration) string {
	secs := int(delay.Round(time.Second).Seconds())
	if attempt <= 2 {
		return fmt.Sprintf("Connection lost. Retrying in %ds.", secs)
	}
	return fmt.Sprintf("Still offline. Check your internet connection. Retrying in %ds.", secs)
}
