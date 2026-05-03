package wa

import (
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
)

func (c *Client) handleEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Connected:
		c.daemon.SetConnMeta(0, 0, false)
		c.daemon.SetStateDetail(app.StateOnline, "Connected to WhatsApp")
		ctx := c.backgroundContext()
		c.syncPresence(ctx, true)
		c.signalSendQueue()
		go c.migrateLIDChats(ctx)
		c.scheduleAvatarRefresh(ctx, 5*time.Second)
	case *events.AppStateSyncComplete:
		c.syncPresence(c.backgroundContext(), true)
	case *events.Disconnected:
		c.daemon.SetConnMeta(0, 0, true)
		c.daemon.SetStateDetail(app.StateReconnecting, "Connection lost. Reconnecting...")
		c.signalReconnect()
	case *events.PairSuccess:
		c.daemon.SetStateDetail(app.StateConnecting, "QR scanned; pairing succeeded")
	case *events.PairError:
		c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("Pairing failed: %v", evt.Error))
	case *events.QRScannedWithoutMultidevice:
		c.daemon.SetStateDetail(app.StateNeedLogin, "Enable multi-device on your phone and scan again")
	case *events.LoggedOut:
		c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("Logged out: %s", evt.Reason.String()))
		go c.resetAfterExternalLogout()
	case *events.ConnectFailure:
		c.daemon.SetConnMeta(0, 0, true)
		c.daemon.SetStateDetail(app.StateOffline, fmt.Sprintf("WhatsApp connection failed: %s", evt.Reason.String()))
		c.signalReconnect()
	case *events.ClientOutdated:
		c.daemon.SetConnMeta(0, 0, false)
		c.daemon.SetStateDetail(app.StateOffline, "WhatsApp client is outdated. Update whatevr/whatevrd.")
	case *events.TemporaryBan:
		c.daemon.SetConnMeta(0, 0, false)
		c.daemon.SetStateDetail(app.StateOffline, evt.String())
	case *events.Message:
		c.handleMessage(c.backgroundContext(), evt)
	case *events.Receipt:
		c.handleReceipt(evt)
	case *events.HistorySync:
		c.handleHistorySync(evt)
	case *events.ChatPresence:
		chatJID := c.normalizeJIDForChat(c.backgroundContext(), evt.Chat)
		isComposing := evt.State == types.ChatPresenceComposing
		c.daemon.PublishChatPresence(chatJID.String(), evt.Sender.String(), isComposing)
	}
}
