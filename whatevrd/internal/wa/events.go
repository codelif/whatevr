package wa

import (
	"fmt"

	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
)

func (c *Client) handleEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Connected:
		c.daemon.SetStateDetail(app.StateOnline, "connected to WhatsApp")
	case *events.Disconnected:
		c.daemon.SetStateDetail(app.StateReconnecting, "WhatsApp connection dropped; reconnecting")
	case *events.PairSuccess:
		c.daemon.SetStateDetail(app.StateConnecting, "QR scanned; pairing succeeded")
	case *events.PairError:
		c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("pairing failed: %v", evt.Error))
	case *events.QRScannedWithoutMultidevice:
		c.daemon.SetStateDetail(app.StateNeedLogin, "enable multi-device on phone and scan again")
	case *events.LoggedOut:
		c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("logged out: %s", evt.Reason.String()))
	case *events.ConnectFailure:
		c.daemon.SetStateDetail(app.StateOffline, fmt.Sprintf("connect failed: %s", evt.Reason.String()))
	case *events.ClientOutdated:
		c.daemon.SetStateDetail(app.StateOffline, "WhatsApp client is outdated")
	case *events.TemporaryBan:
		c.daemon.SetStateDetail(app.StateOffline, evt.String())
	case *events.Message:
		c.handleMessage(evt)
	}
}
