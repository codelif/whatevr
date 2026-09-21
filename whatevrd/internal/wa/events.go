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

// handleEvent returns whether whatsmeow may ack the event. Only a message that
// could not be stored answers false: the ack goes unsent and the server delivers
// it again, instead of the message being lost with nothing but a log line.
func (c *Client) handleEvent(sess *accountSession, raw any) bool {
	// whatsmeow dispatches events from a single queue that waits for each
	// handler to return, so a slow handler here delays every event behind it
	// (incoming messages, receipts, presence).
	start := time.Now()
	defer func() {
		if d := time.Since(start); d > slowEventHandlerThreshold {
			c.log.Warnf("Slow event handler: %T took %s", raw, d.Round(time.Millisecond))
		}
	}()

	if !sess.alive() {
		return true
	}
	if evt, ok := raw.(*events.OfflineSyncPreview); ok {
		c.handleOfflineSyncPreview(evt)
		return true
	}
	if evt, ok := raw.(*events.OfflineSyncCompleted); ok {
		c.handleOfflineSyncCompleted(evt)
		return true
	}
	offlineSync := c.offlineSyncInProgress()
	if offlineSync {
		defer c.recordOfflineSyncEvent()
	}

	switch evt := raw.(type) {
	case *events.Connected:
		c.daemon.SetConnection(app.StateOnline, "Connected to WhatsApp", 0, 0, false)
		ctx := sess.detached()
		c.syncPresence(ctx, true)
		c.signalSendQueue()
		c.signalHistorySyncWorker()
		c.startAppStateReconcile()
		c.startUnresolvedGroupNameBackfill(ctx)
		// The store needs to know who we are to mark our own vote in a poll
		// tally. It changes once a login, so record it here rather than asking
		// the network layer on every page of messages.
		c.recordSelfJID(ctx)
		sess.spawn(c.migrateLIDChats)
		sess.spawn(c.backfillAnimatedWebPFlags)
		sess.spawn(c.backfillStatusThumbs)
		sess.spawn(c.pruneStatusBroadcastMirror)
		sess.spawn(c.pruneMisfiledNewsletterChats)
	case *events.AppStateSyncComplete:
		c.syncPresence(sess.detached(), true)
		// A fresh login reconciles app state on Connected, which is before the
		// device has any: the snapshot comes back empty, and ReconcileChatPins
		// is full authority, so it concludes nothing is pinned. This is the
		// moment the state actually exists, and nothing was re-reading it, so a
		// first sync finished with every pin dropped.
		if evt.Name == appstate.WAPatchRegularLow || evt.Name == appstate.WAPatchRegularHigh {
			c.startAppStateReconcile()
		}
	case *events.AppState:
		// Typed app-state events (pins, mutes...) have their own cases;
		// sticker favorites/recents only arrive through this generic event.
		c.handleStickerAppState(sess.detached(), evt)
	case *events.AppStateSyncError:
		if evt.Name == appstate.WAPatchRegularLow && isAppStateConflictError(evt.Error) {
			c.log.Warnf("WhatsApp regular_low app state sync failed; recovering pinned chats: %v", evt.Error)
			c.startPinnedChatRecoveryFromAppState()
		}
	case *events.Disconnected:
		if c.connectionIsLive() {
			// whatsmeow dispatches this on its own goroutine, so a Disconnected
			// for a socket that is already gone can land after its replacement
			// is up. The client itself is the authority, not arrival order.
			c.log.Debugf("Ignoring a disconnect for a socket that is already replaced")
			break
		}
		c.daemon.SetConnection(app.StateReconnecting, "Connection lost. Reconnecting...", 0, 0, true)
		c.requestReconnect(false)
	case *events.KeepAliveTimeout:
		if c.connectionIsLive() {
			c.log.Debugf("Ignoring a keepalive timeout for a socket that is already replaced")
			break
		}
		c.daemon.SetConnection(app.StateOffline, "Connection lost. Reconnecting...", 0, 0, true)
		c.requestReconnect(true)
	case *events.KeepAliveRestored:
		client := c.currentClient()
		if client != nil && client.IsLoggedIn() && client.IsConnected() {
			c.daemon.SetConnection(app.StateOnline, "Connected to WhatsApp", 0, 0, false)
		}
	case *events.PairSuccess:
		c.daemon.SetStateDetail(app.StateConnecting, "QR scanned; pairing succeeded")
	case *events.PairError:
		c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("Pairing failed: %v", evt.Error))
	case *events.QRScannedWithoutMultidevice:
		c.daemon.SetStateDetail(app.StateNeedLogin, "Enable multi-device on your phone and scan again")
	case *events.LoggedOut:
		c.daemon.SetStateDetail(app.StateNeedLogin, fmt.Sprintf("Logged out: %s", evt.Reason.String()))
		// Not on the session: this ends it, and spawn would wait on itself.
		go c.resetAfterExternalLogout()
	case *events.ConnectFailure:
		c.daemon.SetConnection(app.StateOffline, fmt.Sprintf("WhatsApp connection failed: %s", evt.Reason.String()), 0, 0, true)
		c.requestReconnect(true)
	case *events.ClientOutdated:
		c.daemon.SetConnection(app.StateOffline, "WhatsApp client is outdated. Update whatevr/whatevrd.", 0, 0, false)
	case *events.TemporaryBan:
		c.daemon.SetConnection(app.StateOffline, evt.String(), 0, 0, false)
	case *events.Message:
		return c.handleMessage(sess.detached(), evt, offlineSync)
	case *events.CallOffer:
		c.handleCallOffer(sess.detached(), evt)
	case *events.CallOfferNotice:
		c.handleCallOfferNotice(sess.detached(), evt)
	case *events.CallTerminate:
		c.handleCallTerminate(sess.detached(), evt)
	case *events.CallReject:
		c.handleCallReject(sess.detached(), evt)
	case *events.UndecryptableMessage:
		c.handleUndecryptableMessage(sess.detached(), evt)
	case *events.Receipt:
		c.handleReceipt(evt, offlineSync)
	case *events.HistorySync:
		c.handleHistorySync(sess, evt)
	case *events.MediaRetry:
		c.handleMediaRetry(sess.detached(), evt)
	case *events.Pin:
		c.handlePinEvent(sess.detached(), evt)
	case *events.Archive:
		c.handleArchiveEvent(sess.detached(), evt)
	case *events.Mute:
		c.handleMuteEvent(sess.detached(), evt)
	case *events.UserStatusMute:
		c.handleUserStatusMuteEvent(sess.detached(), evt)
	case *events.Star:
		c.handleStarEvent(sess.detached(), evt)
	case *events.MarkChatAsRead:
		c.handleMarkChatAsReadEvent(sess.detached(), evt)
	case *events.DeleteForMe:
		c.handleDeleteForMeEvent(sess.detached(), evt)
	case *events.DeleteChat:
		c.handleDeleteChatEvent(sess.detached(), evt)
	case *events.ClearChat:
		c.handleClearChatEvent(sess.detached(), evt)
	case *events.JoinedGroup:
		c.handleJoinedGroup(sess.detached(), evt)
	case *events.GroupInfo:
		c.handleGroupInfoEvent(sess.detached(), evt)
	case *events.Picture:
		c.handlePictureEvent(sess.detached(), evt)
		c.recordGroupPhotoChange(sess.detached(), evt)
		if c.isSelfJID(evt.JID) {
			// Our own profile photo changed: refresh the settings profile page.
			c.daemon.PublishSelfProfileChanged()
		}
	case *events.ChatPresence:
		chatJID := c.normalizeJIDForChat(sess.detached(), evt.Chat)
		isComposing := evt.State == types.ChatPresenceComposing
		c.log.Infof("Received WhatsApp chat presence event: chat=%s sender=%s state=%s media=%s composing=%t", chatJID, evt.Sender, evt.State, evt.Media, isComposing)
		c.daemon.PublishChatPresence(chatJID.String(), evt.Sender.String(), isComposing)
	case *events.IdentityChange:
		// A contact's Signal identity changed (they reinstalled/re-registered
		// WhatsApp). With auto-trust on (the default) whatsmeow has already
		// dropped the stale identity and session, so traffic self-heals; log the
		// "security code changed" fact and publish it so a frontend notice can
		// render without further daemon changes.
		jid := c.normalizeJIDForChat(sess.detached(), evt.JID)
		c.log.Warnf("WhatsApp identity changed for %s (implicit=%t)", jid, evt.Implicit)
		c.daemon.PublishIdentityChanged(jid.ToNonAD().String())
		// The notice above is transient. The transcript keeps the fact, because
		// "when did this change" is the question somebody asks about a security
		// code, and a banner that has already gone cannot answer it.
		c.recordIdentityChange(sess.detached(), evt)
	case *events.Presence:
		chatJID := c.normalizeJIDForChat(sess.detached(), evt.From)
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
		// categories (evt.NewSettings is empty), so the full, already-updated
		// settings are read instead of the event.
		c.signalPrivacySettingsPublish()
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

	return true
}

// connectionIsLive asks the client rather than trusting which event arrived
// last. It is deliberately strict: only a socket that is both connected and
// logged in counts, so this can never talk the daemon into claiming an online
// state it does not have.
func (c *Client) connectionIsLive() bool {
	client := c.currentClient()
	return client != nil && client.Store != nil && client.Store.ID != nil &&
		client.IsConnected() && client.IsLoggedIn()
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
