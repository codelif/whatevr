package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

const slowEventHandlerThreshold = 200 * time.Millisecond

func (c *Client) handleEvent(eventGen uint64, raw any) {
	// whatsmeow dispatches events from a single queue that waits for each
	// handler to return, so a slow handler here delays every event behind it
	// (incoming messages, receipts, presence).
	start := time.Now()
	defer func() {
		if d := time.Since(start); d > slowEventHandlerThreshold {
			c.log.Warnf("Slow event handler: %T took %s", raw, d.Round(time.Millisecond))
		}
	}()

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
		c.startAppStateReconcile(ctx)
		c.startUnresolvedGroupNameBackfill(ctx)
		go c.migrateLIDChats(ctx)
		go c.backfillAnimatedWebPFlags(c.backgroundContext())
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
	case *events.Archive:
		c.handleArchiveEvent(c.backgroundContext(), evt)
	case *events.Mute:
		c.handleMuteEvent(c.backgroundContext(), evt)
	case *events.Star:
		c.handleStarEvent(c.backgroundContext(), evt)
	case *events.MarkChatAsRead:
		c.handleMarkChatAsReadEvent(c.backgroundContext(), evt)
	case *events.JoinedGroup:
		c.handleJoinedGroup(c.backgroundContext(), evt)
	case *events.GroupInfo:
		c.handleGroupInfoEvent(c.backgroundContext(), evt)
	case *events.Picture:
		c.handlePictureEvent(c.backgroundContext(), evt)
		if c.isSelfJID(evt.JID) {
			// Our own profile photo changed: refresh the settings profile page.
			c.daemon.PublishSelfProfileChanged()
		}
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
	case *events.PrivacySettings:
		// Privacy changed (here or on the phone): push a fresh snapshot so an
		// open settings window updates live. The event only carries the changed
		// categories (evt.NewSettings is empty), so read the full, already-updated
		// settings from the cache rather than the event.
		if settings, err := c.GetPrivacySettings(c.backgroundContext()); err == nil {
			c.daemon.PublishPrivacySettingsChanged(settings)
		} else {
			c.log.Warnf("Failed to read privacy settings after change event: %v", err)
		}
	case *events.UserAbout:
		// A user's About/status changed. For our own account, re-fetch the self
		// profile: that path resolves the status with the same normalized JID the
		// profile page is keyed on (evt.JID may be a raw PN/LID that won't match).
		// For others, patch their contact card directly.
		if c.isSelfJID(evt.JID) {
			c.daemon.PublishSelfProfileChanged()
		} else {
			c.daemon.PublishContactInfoUpdated(app.ContactInfo{
				JID:        evt.JID.ToNonAD().String(),
				StatusText: evt.Status,
			})
		}
	case *events.PushNameSetting:
		// Our own display name was changed (on the phone): refetch self profile.
		c.daemon.PublishSelfProfileChanged()
	case *events.Blocklist:
		c.daemon.PublishBlocklistChanged()
	}
}

// isSelfJID reports whether jid is the logged-in user's own account.
func (c *Client) isSelfJID(jid types.JID) bool {
	client := c.currentClient()
	if client == nil || client.Store.ID == nil {
		return false
	}
	return jid.ToNonAD().User == client.Store.ID.ToNonAD().User
}

func (c *Client) handlePinEvent(ctx context.Context, evt *events.Pin) {
	if evt == nil || evt.JID.IsEmpty() || evt.Action == nil {
		return
	}

	pinned := evt.Action.GetPinned()
	order := uint32(0)
	if pinned && !evt.Timestamp.IsZero() {
		order = uint32(evt.Timestamp.Unix())
	}

	chatJID, resolved := c.resolveAppStateChatJID(ctx, evt.JID)
	if !resolved {
		c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
			e.hasPin, e.pinned, e.pinOrder = true, pinned, order
		})
		return
	}
	chatID := chatJID.String()
	name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
	if chatJID.Server == types.GroupServer && nameSource == "" {
		nameSource = appstore.ChatNameSourceGroup
	}
	if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
		c.log.Warnf("Failed to ensure pinned chat %s: %v", chatID, err)
		return
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

func (c *Client) handleArchiveEvent(ctx context.Context, evt *events.Archive) {
	if evt == nil || evt.JID.IsEmpty() || evt.Action == nil {
		return
	}

	chatJID, resolved := c.resolveAppStateChatJID(ctx, evt.JID)
	if !resolved {
		archived := evt.Action.GetArchived()
		c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
			e.hasArchive, e.archived = true, archived
		})
		return
	}
	chatID := chatJID.String()
	name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
	if chatJID.Server == types.GroupServer && nameSource == "" {
		nameSource = appstore.ChatNameSourceGroup
	}
	if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
		c.log.Warnf("Failed to ensure archived chat %s: %v", chatID, err)
		return
	}

	chat, changed, err := c.store.UpdateChatArchiveState(ctx, chatID, evt.Action.GetArchived())
	if err != nil {
		c.log.Warnf("Failed to update archive state for %s: %v", chatID, err)
		return
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
}

func (c *Client) handleMuteEvent(ctx context.Context, evt *events.Mute) {
	if evt == nil || evt.JID.IsEmpty() || evt.Action == nil {
		return
	}

	muted := evt.Action.GetMuted()
	var muteEnd int64
	if muted {
		// MuteEndTimestamp is unix millis; 0 from the device means "forever".
		muteEnd = evt.Action.GetMuteEndTimestamp()
		if muteEnd == 0 {
			muteEnd = -1
		}
	}

	chatJID, resolved := c.resolveAppStateChatJID(ctx, evt.JID)
	if !resolved {
		c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
			e.hasMute, e.muted, e.muteEnd = true, muted, muteEnd
		})
		return
	}
	chatID := chatJID.String()
	name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
	if chatJID.Server == types.GroupServer && nameSource == "" {
		nameSource = appstore.ChatNameSourceGroup
	}
	if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
		c.log.Warnf("Failed to ensure muted chat %s: %v", chatID, err)
		return
	}

	chat, changed, err := c.store.UpdateChatMuteState(ctx, chatID, muted, muteEnd)
	if err != nil {
		c.log.Warnf("Failed to update mute state for %s: %v", chatID, err)
		return
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
}

// handleMarkChatAsReadEvent mirrors a chat being marked read (or unread) on the
// phone or another linked device onto the local unread state.
func (c *Client) handleMarkChatAsReadEvent(ctx context.Context, evt *events.MarkChatAsRead) {
	if evt == nil || evt.JID.IsEmpty() || evt.Action == nil {
		return
	}

	read := evt.Action.GetRead()
	uptoUnix := markChatAsReadHorizon(evt)

	chatJID, resolved := c.resolveAppStateChatJID(ctx, evt.JID)
	if !resolved {
		c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
			e.hasMarkRead, e.markRead, e.markReadUpTo = true, read, uptoUnix
		})
		return
	}
	c.applyMarkChatAsRead(ctx, chatJID.String(), read, uptoUnix)
}

func markChatAsReadHorizon(evt *events.MarkChatAsRead) int64 {
	// Only messages up to the action's own horizon are marked read, so a
	// replayed (full-sync/recovery) action can't swallow newer messages.
	uptoUnix := time.Now().Unix()
	if evt == nil {
		return uptoUnix
	}
	if !evt.Timestamp.IsZero() {
		uptoUnix = evt.Timestamp.Unix()
	}
	if raw := evt.Action.GetMessageRange().GetLastMessageTimestamp(); raw > 0 {
		if ts, ok := whatsAppUnixTimestamp(uint64(raw)); ok {
			uptoUnix = ts.Unix()
		}
	}
	return uptoUnix
}

// applyMarkChatAsRead applies a mark-read/mark-unread action to a chat we may
// or may not know about. Unlike pins/mutes it never creates a chat row: read
// state on an unknown chat carries no information worth materializing.
func (c *Client) applyMarkChatAsRead(ctx context.Context, chatID string, read bool, uptoUnix int64) {
	if chatID == "" {
		return
	}
	if read {
		chat, changed, err := c.store.MarkChatReadUpTo(ctx, chatID, uptoUnix)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				c.log.Warnf("Failed to mark chat %s read from app state: %v", chatID, err)
			}
			return
		}
		if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
		return
	}

	// Marked unread on the phone: WhatsApp shows a dot, not a count. Approximate
	// with a badge of 1 unless real unread messages already exist.
	chat, err := c.store.GetChat(ctx, chatID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warnf("Failed to load chat %s for mark-unread: %v", chatID, err)
		}
		return
	}
	if chat.UnreadCount > 0 {
		return
	}
	updated, changed, err := c.store.OverwriteChatUnreadCount(ctx, chatID, 1)
	if err != nil {
		c.log.Warnf("Failed to mark chat %s unread from app state: %v", chatID, err)
		return
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updated))
	}
}

func (c *Client) isCurrentEventGeneration(eventGen uint64) bool {
	return eventGen != 0 && eventGen == c.eventGen.Load()
}
