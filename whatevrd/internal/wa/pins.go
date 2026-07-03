package wa

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	appstore "whatevrd/internal/store"
)

// startAppStateReconcile fetches the full regular app-state snapshots and
// reconciles pins, archives, mutes and sticker favorites. It runs on every
// Connected event, so chats carry their pinned/archived/muted state within
// seconds of connecting — long before history sync finishes. LID chats whose
// PN mapping hasn't landed yet are parked (see reconcilePendingAppState)
// instead of being created as orphan @lid rows.
func (c *Client) startAppStateReconcile(ctx context.Context) {
	c.startPinnedChatRecovery(ctx, "reconcile", c.reconcileRegularAppState)
}

// reconcileAfterHistorySync runs once the initial history sync has settled,
// when whatsmeow has populated its LID→PN mappings from the synced messages.
// It merges any chats that were created under a raw @lid JID into their
// canonical phone-number chat, then applies any app-state entries that were
// parked because their mapping wasn't available at connect time.
func (c *Client) reconcileAfterHistorySync(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	c.migrateLIDChats(ctx)
	c.reconcilePendingAppState(ctx, true)
}

// pendingAppStateEntry parks pin/archive/mute state for a chat whose JID
// could not be attached to a canonical chat yet.
type pendingAppStateEntry struct {
	hasPin       bool
	pinned       bool
	pinOrder     uint32
	hasArchive   bool
	archived     bool
	hasMute      bool
	muted        bool
	muteEnd      int64
	hasMarkRead  bool
	markRead     bool
	markReadUpTo int64
}

// resolveAppStateChatJID normalizes an app-state chat JID and reports whether
// state can be applied to a chat row now. A LID with no PN mapping is still
// applicable if a chat row already exists under the LID (a genuine LID-only
// contact); otherwise the caller must park the state instead of ensuring an
// orphan @lid row that would later duplicate the canonical PN chat.
func (c *Client) resolveAppStateChatJID(ctx context.Context, jid types.JID) (types.JID, bool) {
	normalized := c.normalizeJIDForChat(ctx, jid)
	if normalized.Server != types.HiddenUserServer {
		return normalized, true
	}
	if _, err := c.store.GetChat(ctx, normalized.String()); err == nil {
		return normalized, true
	}
	return normalized, false
}

func (c *Client) parkPendingAppState(jid types.JID, mutate func(*pendingAppStateEntry)) {
	jid = jid.ToNonAD()
	if jid.IsEmpty() {
		return
	}
	c.pendingAppStateMu.Lock()
	if c.pendingAppState == nil {
		c.pendingAppState = make(map[types.JID]pendingAppStateEntry)
	}
	entry := c.pendingAppState[jid]
	mutate(&entry)
	c.pendingAppState[jid] = entry
	c.pendingAppStateMu.Unlock()
}

// reconcilePendingAppState retries parked pin/archive/mute entries. It runs
// as LID→PN mappings land (after each processed history sync chunk and after
// LID chat migration) and once with final=true when the initial sync has
// settled: at that point an entry that still has no mapping belongs to a
// genuine LID-only contact and is applied to the LID chat itself.
func (c *Client) reconcilePendingAppState(ctx context.Context, final bool) {
	c.pendingAppStateMu.Lock()
	if len(c.pendingAppState) == 0 {
		c.pendingAppStateMu.Unlock()
		return
	}
	pending := c.pendingAppState
	c.pendingAppState = nil
	c.pendingAppStateMu.Unlock()

	for jid, entry := range pending {
		if ctx.Err() != nil {
			return
		}
		resolved, ok := c.resolveAppStateChatJID(ctx, jid)
		if !ok && !final {
			// Still unresolved: park again, without clobbering state that
			// arrived while this pass was running (newer state wins).
			parked := entry
			c.parkPendingAppState(jid, func(e *pendingAppStateEntry) {
				if !e.hasPin && parked.hasPin {
					e.hasPin, e.pinned, e.pinOrder = true, parked.pinned, parked.pinOrder
				}
				if !e.hasArchive && parked.hasArchive {
					e.hasArchive, e.archived = true, parked.archived
				}
				if !e.hasMute && parked.hasMute {
					e.hasMute, e.muted, e.muteEnd = true, parked.muted, parked.muteEnd
				}
				if !e.hasMarkRead && parked.hasMarkRead {
					e.hasMarkRead, e.markRead, e.markReadUpTo = true, parked.markRead, parked.markReadUpTo
				}
			})
			continue
		}
		c.applyPendingAppState(ctx, resolved, entry)
	}
}

func (c *Client) applyPendingAppState(ctx context.Context, chatJID types.JID, entry pendingAppStateEntry) {
	chatID := chatJID.String()
	if chatID == "" {
		return
	}
	name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
	if chatJID.Server == types.GroupServer && nameSource == "" {
		nameSource = appstore.ChatNameSourceGroup
	}
	if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
		c.log.Warnf("Failed to ensure chat %s for deferred app state: %v", chatID, err)
		return
	}
	if entry.hasPin {
		if chat, changed, err := c.store.UpdateChatPinState(ctx, chatID, entry.pinned, entry.pinOrder); err != nil {
			c.log.Warnf("Failed to apply deferred pin state for %s: %v", chatID, err)
		} else if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
	if entry.hasArchive {
		if chat, changed, err := c.store.UpdateChatArchiveState(ctx, chatID, entry.archived); err != nil {
			c.log.Warnf("Failed to apply deferred archive state for %s: %v", chatID, err)
		} else if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
	if entry.hasMute {
		if chat, changed, err := c.store.UpdateChatMuteState(ctx, chatID, entry.muted, entry.muteEnd); err != nil {
			c.log.Warnf("Failed to apply deferred mute state for %s: %v", chatID, err)
		} else if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
	if entry.hasMarkRead {
		c.applyMarkChatAsRead(ctx, chatID, entry.markRead, entry.markReadUpTo)
	}
}

func (c *Client) startPinnedChatRecovery(ctx context.Context, reason string, fn func(context.Context) error) {
	if !c.pinBackfill.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer c.pinBackfill.Store(false)
		if err := fn(ctx); err != nil {
			c.log.Warnf("Failed to %s pinned chats from WhatsApp app state: %v", reason, err)
		}
	}()
}

func (c *Client) startPinnedChatRecoveryFromAppState(ctx context.Context) {
	c.startPinnedChatRecovery(ctx, "recover", c.recoverPinnedChatsFromAppState)
}

func (c *Client) reconcileRegularAppState(ctx context.Context) error {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil
	}

	c.appStateMu.Lock()
	defer c.appStateMu.Unlock()
	eventsToDispatch, err := fetchFullRegularLowAppState(ctx, client)
	if err != nil {
		if !isAppStateConflictError(err) {
			return err
		}
		c.log.Warnf("WhatsApp regular_low app state snapshot verification failed while reconciling; requesting recovery: %v", err)
		eventsToDispatch, err = recoverRegularLowAppState(ctx, client)
		if err != nil {
			return err
		}
	}

	// The full regular_low snapshot also carries sticker favorites and chat
	// archive state; reconcile them here so one fetch serves all features.
	c.reconcileFavoriteStickersFromEvents(ctx, eventsToDispatch)
	if err := c.reconcileArchivedChatsFromEvents(ctx, eventsToDispatch); err != nil {
		c.log.Warnf("Failed to reconcile archived chats from app state: %v", err)
	}
	c.reconcileMarkReadFromEvents(ctx, eventsToDispatch)
	if err := c.reconcilePinnedChatsFromEvents(ctx, eventsToDispatch); err != nil {
		return err
	}

	// Chat mutes live in the regular_high app state.
	highEvents, err := fetchFullRegularHighAppState(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to fetch regular_high app state: %w", err)
	}
	return c.reconcileMutedChatsFromEvents(ctx, highEvents)
}

func (c *Client) recoverPinnedChatsFromAppState(ctx context.Context) error {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil
	}

	c.appStateMu.Lock()
	defer c.appStateMu.Unlock()
	eventsToDispatch, err := recoverRegularLowAppState(ctx, client)
	if err != nil {
		return err
	}
	c.reconcileFavoriteStickersFromEvents(ctx, eventsToDispatch)
	if err := c.reconcileArchivedChatsFromEvents(ctx, eventsToDispatch); err != nil {
		c.log.Warnf("Failed to reconcile archived chats from app state: %v", err)
	}
	c.reconcileMarkReadFromEvents(ctx, eventsToDispatch)
	return c.reconcilePinnedChatsFromEvents(ctx, eventsToDispatch)
}

func (c *Client) reconcileMarkReadFromEvents(ctx context.Context, eventsToDispatch []any) {
	for _, raw := range eventsToDispatch {
		evt, ok := raw.(*events.MarkChatAsRead)
		if !ok || evt.JID.IsEmpty() || evt.Action == nil {
			continue
		}
		chatJID, resolved := c.resolveAppStateChatJID(ctx, evt.JID)
		read := evt.Action.GetRead()
		uptoUnix := markChatAsReadHorizon(evt)
		if !resolved {
			c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
				e.hasMarkRead, e.markRead, e.markReadUpTo = true, read, uptoUnix
			})
			continue
		}
		c.applyMarkChatAsRead(ctx, chatJID.String(), read, uptoUnix)
	}
}

func (c *Client) reconcileArchivedChatsFromEvents(ctx context.Context, eventsToDispatch []any) error {
	archived := make(map[string]struct{})
	for chatJID := range archivedChatsFromEvents(eventsToDispatch) {
		chatJID, resolved := c.resolveAppStateChatJID(ctx, chatJID)
		if !resolved {
			c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
				e.hasArchive, e.archived = true, true
			})
			continue
		}
		chatID := chatJID.String()
		if chatID == "" {
			continue
		}
		name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
		if chatJID.Server == types.GroupServer && nameSource == "" {
			nameSource = appstore.ChatNameSourceGroup
		}
		if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
			c.log.Warnf("Failed to ensure archived chat %s: %v", chatID, err)
			continue
		}
		archived[chatID] = struct{}{}
	}

	changed, err := c.store.ReconcileChatArchives(ctx, archived)
	if err != nil {
		return err
	}
	for _, chat := range changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return nil
}

func archivedChatsFromEvents(eventsToDispatch []any) map[types.JID]struct{} {
	archived := make(map[types.JID]struct{})
	for _, raw := range eventsToDispatch {
		evt, ok := raw.(*events.Archive)
		if !ok || evt.JID.IsEmpty() || evt.Action == nil {
			continue
		}
		if evt.Action.GetArchived() {
			archived[evt.JID] = struct{}{}
		} else {
			delete(archived, evt.JID)
		}
	}
	return archived
}

func (c *Client) reconcilePinnedChatsFromEvents(ctx context.Context, eventsToDispatch []any) error {
	pins := make(map[string]uint32)
	for chatJID, order := range pinnedChatsFromEvents(eventsToDispatch) {
		chatJID, resolved := c.resolveAppStateChatJID(ctx, chatJID)
		if !resolved {
			pinOrder := order
			c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
				e.hasPin, e.pinned, e.pinOrder = true, true, pinOrder
			})
			continue
		}
		chatID := chatJID.String()
		if chatID == "" {
			continue
		}
		name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
		if chatJID.Server == types.GroupServer && nameSource == "" {
			nameSource = appstore.ChatNameSourceGroup
		}
		if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
			c.log.Warnf("Failed to ensure pinned chat %s: %v", chatID, err)
			continue
		}
		pins[chatID] = order
	}

	changed, err := c.store.ReconcileChatPins(ctx, pins)
	if err != nil {
		return err
	}
	for _, chat := range changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return nil
}

func pinnedChatsFromEvents(eventsToDispatch []any) map[types.JID]uint32 {
	pins := make(map[types.JID]uint32)
	for _, raw := range eventsToDispatch {
		pin, ok := raw.(*events.Pin)
		if !ok || pin.JID.IsEmpty() || pin.Action == nil {
			continue
		}
		if !pin.Action.GetPinned() {
			delete(pins, pin.JID)
			continue
		}

		order := uint32(0)
		if !pin.Timestamp.IsZero() {
			order = uint32(pin.Timestamp.Unix())
		}
		pins[pin.JID] = order
	}
	return pins
}

func (c *Client) reconcileMutedChatsFromEvents(ctx context.Context, eventsToDispatch []any) error {
	mutes := make(map[string]int64)
	for chatJID, muteEnd := range mutedChatsFromEvents(eventsToDispatch) {
		chatJID, resolved := c.resolveAppStateChatJID(ctx, chatJID)
		if !resolved {
			end := muteEnd
			c.parkPendingAppState(chatJID, func(e *pendingAppStateEntry) {
				e.hasMute, e.muted, e.muteEnd = true, true, end
			})
			continue
		}
		chatID := chatJID.String()
		if chatID == "" {
			continue
		}
		name, nameSource := c.displayNameForChat(ctx, chatJID, false, "", "")
		if chatJID.Server == types.GroupServer && nameSource == "" {
			nameSource = appstore.ChatNameSourceGroup
		}
		if _, err := c.store.EnsureChatWithNameSource(ctx, chatID, name, nameSource, chatJID.Server == types.GroupServer); err != nil {
			c.log.Warnf("Failed to ensure muted chat %s: %v", chatID, err)
			continue
		}
		mutes[chatID] = muteEnd
	}

	changed, err := c.store.ReconcileChatMutes(ctx, mutes)
	if err != nil {
		return err
	}
	for _, chat := range changed {
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
	return nil
}

func mutedChatsFromEvents(eventsToDispatch []any) map[types.JID]int64 {
	mutes := make(map[types.JID]int64)
	for _, raw := range eventsToDispatch {
		evt, ok := raw.(*events.Mute)
		if !ok || evt.JID.IsEmpty() || evt.Action == nil {
			continue
		}
		if !evt.Action.GetMuted() {
			delete(mutes, evt.JID)
			continue
		}
		// MuteEndTimestamp is unix millis; 0 from the device means "forever",
		// stored as -1 (matches handleMuteEvent).
		muteEnd := evt.Action.GetMuteEndTimestamp()
		if muteEnd == 0 {
			muteEnd = -1
		}
		mutes[evt.JID] = muteEnd
	}
	return mutes
}

func fetchFullRegularLowAppState(ctx context.Context, client *whatsmeow.Client) ([]any, error) {
	oldEmit := client.EmitAppStateEventsOnFullSync
	client.EmitAppStateEventsOnFullSync = true
	defer func() { client.EmitAppStateEventsOnFullSync = oldEmit }()

	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return client.DangerousInternals().FetchAppState(fetchCtx, appstate.WAPatchRegularLow, true, false)
}

func recoverRegularLowAppState(ctx context.Context, client *whatsmeow.Client) ([]any, error) {
	recoveryCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	oldEmit := client.EmitAppStateEventsOnFullSync
	client.EmitAppStateEventsOnFullSync = true
	defer func() { client.EmitAppStateEventsOnFullSync = oldEmit }()

	var mu sync.Mutex
	var eventsToDispatch []any
	var syncErr error
	done := make(chan struct{})
	doneOnce := sync.Once{}
	finish := func() {
		doneOnce.Do(func() { close(done) })
	}

	handlerID := client.AddEventHandler(func(raw any) {
		switch evt := raw.(type) {
		case *events.Pin:
			mu.Lock()
			eventsToDispatch = append(eventsToDispatch, evt)
			mu.Unlock()
		case *events.Archive:
			mu.Lock()
			eventsToDispatch = append(eventsToDispatch, evt)
			mu.Unlock()
		case *events.AppState:
			// Sticker favorites ride in the same regular_low snapshot.
			mu.Lock()
			eventsToDispatch = append(eventsToDispatch, evt)
			mu.Unlock()
		case *events.AppStateSyncComplete:
			if evt.Name == appstate.WAPatchRegularLow && evt.Recovery {
				finish()
			}
		case *events.AppStateSyncError:
			if evt.Name == appstate.WAPatchRegularLow {
				mu.Lock()
				syncErr = evt.Error
				mu.Unlock()
				finish()
			}
		}
	})
	defer client.RemoveEventHandler(handlerID)

	if _, err := client.SendPeerMessage(recoveryCtx, whatsmeow.BuildAppStateRecoveryRequest(appstate.WAPatchRegularLow)); err != nil {
		return nil, fmt.Errorf("failed to request regular_low app state recovery: %w", err)
	}

	select {
	case <-done:
		mu.Lock()
		defer mu.Unlock()
		if syncErr != nil {
			return nil, fmt.Errorf("failed to recover regular_low app state: %w", syncErr)
		}
		return append([]any(nil), eventsToDispatch...), nil
	case <-recoveryCtx.Done():
		return nil, fmt.Errorf("timed out waiting for regular_low app state recovery: %w", recoveryCtx.Err())
	}
}
