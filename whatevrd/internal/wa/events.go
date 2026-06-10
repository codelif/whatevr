package wa

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func (c *Client) handleEvent(eventGen uint64, raw any) {
	if !c.isCurrentEventGeneration(eventGen) {
		return
	}
	if evt, ok := raw.(*events.OfflineSyncPreview); ok {
		c.handleOfflineSyncPreview(evt)
		return
	}
	if evt, ok := raw.(*events.OfflineSyncCompleted); ok {
		c.handleOfflineSyncCompleted(evt)
		return
	}
	offlineSync := c.offlineSyncInProgress()
	if offlineSync {
		defer c.recordOfflineSyncEvent()
	}

	switch evt := raw.(type) {
	case *events.Connected:
		c.daemon.SetConnMeta(0, 0, false)
		c.daemon.SetStateDetail(app.StateOnline, "Connected to WhatsApp")
		ctx := c.backgroundContext()
		c.syncPresence(ctx, true)
		c.signalSendQueue()
		c.signalHistorySyncWorker()
		c.startPinnedChatBackfill(ctx)
		c.startUnresolvedGroupNameBackfill(ctx)
		go c.migrateLIDChats(ctx)
	case *events.AppStateSyncComplete:
		c.syncPresence(c.backgroundContext(), true)
	case *events.AppState:
		// Typed app-state events (pins, mutes...) have their own cases;
		// sticker favorites/recents only arrive through this generic event.
		c.handleStickerAppState(c.backgroundContext(), evt)
	case *events.AppStateSyncError:
		if evt.Name == appstate.WAPatchRegularLow && isAppStateConflictError(evt.Error) {
			c.log.Warnf("WhatsApp regular_low app state sync failed; recovering pinned chats: %v", evt.Error)
			c.startPinnedChatRecoveryFromAppState(c.backgroundContext())
		}
	case *events.Disconnected:
		c.daemon.SetConnMeta(0, 0, true)
		c.daemon.SetStateDetail(app.StateReconnecting, "Connection lost. Reconnecting...")
		c.requestReconnect(false)
	case *events.KeepAliveTimeout:
		c.daemon.SetConnMeta(0, 0, true)
		c.daemon.SetStateDetail(app.StateOffline, "Connection lost. Reconnecting...")
		c.requestReconnect(true)
	case *events.KeepAliveRestored:
		client := c.currentClient()
		if client != nil && client.IsLoggedIn() && client.IsConnected() {
			c.daemon.SetConnMeta(0, 0, false)
			c.daemon.SetStateDetail(app.StateOnline, "Connected to WhatsApp")
		}
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
		c.requestReconnect(true)
	case *events.ClientOutdated:
		c.daemon.SetConnMeta(0, 0, false)
		c.daemon.SetStateDetail(app.StateOffline, "WhatsApp client is outdated. Update whatevr/whatevrd.")
	case *events.TemporaryBan:
		c.daemon.SetConnMeta(0, 0, false)
		c.daemon.SetStateDetail(app.StateOffline, evt.String())
	case *events.Message:
		c.handleMessage(c.backgroundContext(), evt, offlineSync)
	case *events.UndecryptableMessage:
		c.handleUndecryptableMessage(c.backgroundContext(), evt)
	case *events.Receipt:
		c.handleReceipt(evt, offlineSync)
	case *events.HistorySync:
		c.handleHistorySync(eventGen, evt)
	case *events.MediaRetry:
		c.handleMediaRetry(c.backgroundContext(), evt)
	case *events.Pin:
		c.handlePinEvent(c.backgroundContext(), evt)
	case *events.JoinedGroup:
		c.handleJoinedGroup(c.backgroundContext(), evt)
	case *events.GroupInfo:
		c.handleGroupInfoEvent(c.backgroundContext(), evt)
	case *events.Picture:
		c.handlePictureEvent(c.backgroundContext(), evt)
	case *events.ChatPresence:
		chatJID := c.normalizeJIDForChat(c.backgroundContext(), evt.Chat)
		isComposing := evt.State == types.ChatPresenceComposing
		c.log.Infof("Received WhatsApp chat presence event: chat=%s sender=%s state=%s media=%s composing=%t", chatJID, evt.Sender, evt.State, evt.Media, isComposing)
		c.daemon.PublishChatPresence(chatJID.String(), evt.Sender.String(), isComposing)
	case *events.Presence:
		chatJID := c.normalizeJIDForChat(c.backgroundContext(), evt.From)
		availability := app.ContactAvailabilityOnline
		var lastSeenUnix int64
		if evt.Unavailable {
			availability = app.ContactAvailabilityOffline
			if !evt.LastSeen.IsZero() {
				lastSeenUnix = evt.LastSeen.Unix()
			}
		}
		c.daemon.PublishContactAvailability(chatJID.String(), availability, lastSeenUnix)
	}
}

func (c *Client) handlePinEvent(ctx context.Context, evt *events.Pin) {
	if evt == nil || evt.JID.IsEmpty() || evt.Action == nil {
		return
	}

	chatJID := c.normalizeJIDForChat(ctx, evt.JID)
	chatID := chatJID.String()
	name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
	if chatJID.Server == types.GroupServer && nameSource == "" {
		nameSource = appstore.ChatNameSourceGroup
	}
	if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
		c.log.Warnf("Failed to ensure pinned chat %s: %v", chatID, err)
		return
	}

	pinned := evt.Action.GetPinned()
	order := uint32(0)
	if pinned && !evt.Timestamp.IsZero() {
		order = uint32(evt.Timestamp.Unix())
	}
	chat, changed, err := c.store.UpdateChatPinState(ctx, chatID, pinned, order)
	if err != nil {
		c.log.Warnf("Failed to update pinned state for %s: %v", chatID, err)
		return
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
}

func (c *Client) isCurrentEventGeneration(eventGen uint64) bool {
	return eventGen != 0 && eventGen == c.eventGen.Load()
}
