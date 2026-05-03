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
		c.daemon.SetStateDetail(app.StateOnline, "connected to WhatsApp")
		ctx := c.backgroundContext()
		c.syncPresence(ctx, true)
		go c.migrateLIDChats(ctx)
		c.scheduleAvatarRefresh(ctx, 5*time.Second)
	case *events.AppStateSyncComplete:
		c.syncPresence(c.backgroundContext(), true)
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
		go c.resetAfterExternalLogout()
	case *events.ConnectFailure:
		c.daemon.SetStateDetail(app.StateOffline, fmt.Sprintf("connect failed: %s", evt.Reason.String()))
	case *events.ClientOutdated:
		c.daemon.SetStateDetail(app.StateOffline, "WhatsApp client is outdated")
	case *events.TemporaryBan:
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
