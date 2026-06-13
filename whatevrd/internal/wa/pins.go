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

func (c *Client) startPinnedChatBackfill(ctx context.Context) {
	c.startPinnedChatRecovery(ctx, "backfill", c.backfillPinnedChatsFromAppState)
}

// reconcileAfterHistorySync runs once the initial history sync has settled, when
// whatsmeow has populated its LID→PN mappings from the synced messages and the
// regular_low app state is stable. It first merges any chats that were created
// under a raw @lid JID into their canonical phone-number chat, then reconciles
// pins so they attach to those canonical chats. Running this post-sync (rather
// than only on Connected, where the mappings are not yet available) is what makes
// pins, display names and duplicates resolve without a daemon restart.
func (c *Client) reconcileAfterHistorySync(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	c.migrateLIDChats(ctx)
	if err := c.backfillPinnedChatsFromAppState(ctx); err != nil {
		c.log.Warnf("Failed to reconcile pinned chats after history sync: %v", err)
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

func (c *Client) backfillPinnedChatsFromAppState(ctx context.Context) error {
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
		c.log.Warnf("WhatsApp regular_low app state snapshot verification failed while backfilling pins; requesting recovery: %v", err)
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
	return c.reconcilePinnedChatsFromEvents(ctx, eventsToDispatch)
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
	return c.reconcilePinnedChatsFromEvents(ctx, eventsToDispatch)
}

func (c *Client) reconcileArchivedChatsFromEvents(ctx context.Context, eventsToDispatch []any) error {
	archived := make(map[string]struct{})
	for chatJID := range archivedChatsFromEvents(eventsToDispatch) {
		chatJID = c.normalizeJIDForChat(ctx, chatJID)
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
		chatJID = c.normalizeJIDForChat(ctx, chatJID)
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
