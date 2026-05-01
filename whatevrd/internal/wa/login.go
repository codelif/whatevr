package wa

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"

	"whatevrd/internal/app"
)

func (c *Client) start(ctx context.Context) {
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

func (c *Client) Logout(ctx context.Context) error {
	client := c.currentClient()
	if client == nil {
		return nil
	}

	if client.Store.ID != nil {
		if err := client.Logout(ctx); err != nil && err != store.ErrDeviceDeleted {
			return err
		}
	} else {
		client.Disconnect()
	}

	if err := c.resetClient(ctx); err != nil {
		return err
	}

	c.daemon.SetStateDetail(app.StateNeedLogin, "logged out")
	c.Start(ctx)
	return nil
}
