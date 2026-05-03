package wa

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"

	"whatevrd/internal/app"
)

func (c *Client) start(ctx context.Context) {
	if latestVer, err := whatsmeow.GetLatestVersion(ctx, nil); err != nil {
		c.log.Warnf("Failed to fetch latest WhatsApp version: %v", err)
	} else {
		store.SetWAVersion(*latestVer)
	}

	client := c.currentClient()
	if client == nil {
		c.daemon.SetStateDetail(app.StateOffline, "WhatsApp client is not initialized")
		return
	}

	if client.Store.ID == nil {
		c.startQRLogin(ctx, client)
		return
	}

	c.daemon.SetStateDetail(app.StateConnecting, "connecting to WhatsApp")
	if err := client.ConnectContext(ctx); err != nil {
		c.daemon.SetStateDetail(app.StateOffline, fmt.Sprintf("connect failed: %v", err))
		return
	}

	if client.IsLoggedIn() {
		c.daemon.SetStateDetail(app.StateOnline, "connected to WhatsApp")
	}
}

func (c *Client) startQRLogin(ctx context.Context, client *whatsmeow.Client) {
	c.daemon.SetStateDetail(app.StateNeedLogin, "waiting for WhatsApp QR scan")

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		c.daemon.SetStateDetail(app.StateOffline, fmt.Sprintf("create QR channel: %v", err))
		return
	}

	if err := client.ConnectContext(ctx); err != nil {
		c.daemon.SetStateDetail(app.StateOffline, fmt.Sprintf("connect for QR login failed: %v", err))
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-qrChan:
			if !ok {
				return
			}

			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				c.daemon.PublishQRCode(item.Code, time.Now().Add(item.Timeout))
			case whatsmeow.QRChannelSuccess.Event:
				c.daemon.SetStateDetail(app.StateConnecting, "QR scanned; completing login")
			case whatsmeow.QRChannelTimeout.Event:
				c.daemon.SetStateDetail(app.StateNeedLogin, "QR login timed out; restart daemon to try again")
			case whatsmeow.QRChannelClientOutdated.Event:
				c.daemon.SetStateDetail(app.StateOffline, "WhatsApp client is outdated")
			case whatsmeow.QRChannelScannedWithoutMultidevice.Event:
				c.daemon.SetStateDetail(app.StateNeedLogin, "enable multi-device on phone and scan again")
			case whatsmeow.QRChannelEventError:
				c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("QR login error: %v", item.Error))
			default:
				c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("QR login event: %s", item.Event))
			}
		}
	}
}

func (c *Client) resetAfterExternalLogout() {
	ctx := context.Background()
	c.cancelRunContext()

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
	c.Start(context.Background())
}

func (c *Client) Logout(ctx context.Context) error {
	c.daemon.SetStateDetail(app.StateConnecting, "logging out and clearing local session data")

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
	c.cancelRunContext()

	localCtx := context.Background()

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

	c.daemon.SetStateDetail(app.StateNeedLogin, "logged out")
	c.Start(context.Background())
	return nil
}
