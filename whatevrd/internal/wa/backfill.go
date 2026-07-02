package wa

import (
	"context"
	"errors"
	"time"

	"go.mau.fi/whatsmeow/types"

	appstore "whatevrd/internal/store"
)

const (
	// Recommended request size for on-demand history (whatsmeow docs).
	backfillRequestCount = 50
	// How long to wait for the phone to answer before the request is
	// forgotten. The phone may be offline; expiring without marking the
	// chat exhausted keeps the affordance retryable.
	backfillRequestTimeout = 90 * time.Second
)

type backfillRequest struct {
	requested int
	timer     *time.Timer
}

// RequestOlderMessages asks the phone for messages older than the oldest one
// stored for the chat (on-demand history sync). Returns false when there was
// nothing to ask: no stored messages, history already exhausted, or a request
// already in flight. The response lands asynchronously through the normal
// history sync pipeline; HistoryBackfilled and ChatUpdated events follow.
func (c *Client) RequestOlderMessages(ctx context.Context, chatID string) (bool, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return false, errors.New("not connected to WhatsApp")
	}

	chat, err := c.store.GetChat(ctx, chatID)
	if err != nil {
		return false, err
	}
	if chat.HistoryExhausted {
		return false, nil
	}
	oldest, ok, err := c.store.OldestStoredMessage(ctx, chatID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	chatJID, err := types.ParseJID(chatID)
	if err != nil {
		return false, err
	}

	c.backfillMu.Lock()
	if c.backfillInFlight == nil {
		c.backfillInFlight = make(map[string]*backfillRequest)
	}
	if _, busy := c.backfillInFlight[chatID]; busy {
		c.backfillMu.Unlock()
		return false, nil
	}
	req := &backfillRequest{requested: backfillRequestCount}
	c.backfillInFlight[chatID] = req
	c.backfillMu.Unlock()

	info := &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chatJID,
			IsFromMe: oldest.Direction == appstore.DirectionOutgoing,
			IsGroup:  chat.IsGroup,
		},
		ID:        appstore.ExternalMessageID(chatID, oldest.ID),
		Timestamp: time.Unix(oldest.TimestampUnix, 0),
	}
	if _, err := client.SendPeerMessage(ctx, client.BuildHistorySyncRequest(info, backfillRequestCount)); err != nil {
		c.finishBackfillRequest(chatID)
		return false, err
	}
	req.timer = time.AfterFunc(backfillRequestTimeout, func() {
		if c.finishBackfillRequest(chatID) {
			c.log.Debugf("On-demand history request for %s expired without a response", chatID)
		}
	})
	c.log.Infof("Requested %d older messages for %s before %s", backfillRequestCount, chatID, info.Timestamp.Format(time.RFC3339))
	return true, nil
}

// finishBackfillRequest drops the in-flight entry for a chat, reporting
// whether one existed.
func (c *Client) finishBackfillRequest(chatID string) bool {
	c.backfillMu.Lock()
	req, ok := c.backfillInFlight[chatID]
	if ok {
		delete(c.backfillInFlight, chatID)
	}
	c.backfillMu.Unlock()
	if ok && req.timer != nil {
		req.timer.Stop()
	}
	return ok
}

// resolveBackfillRequests matches an ON_DEMAND history sync response against
// in-flight requests. messagesByChat holds the raw payload message count per
// (normalized) chat in the chunk. A response carrying fewer messages than
// requested means the phone has nothing older, so the chat's history is
// exhausted. A chunk that doesn't mention the chat at all resolves the same
// way, but only when it is unambiguous (a single request in flight);
// otherwise the request is left to its expiry timer.
func (c *Client) resolveBackfillRequests(ctx context.Context, messagesByChat map[string]int) {
	type resolution struct {
		chatID    string
		exhausted bool
	}
	var resolved []resolution

	c.backfillMu.Lock()
	single := len(c.backfillInFlight) == 1
	for chatID, req := range c.backfillInFlight {
		count, present := messagesByChat[chatID]
		if !present && !single {
			continue
		}
		if req.timer != nil {
			req.timer.Stop()
		}
		delete(c.backfillInFlight, chatID)
		resolved = append(resolved, resolution{chatID: chatID, exhausted: count < req.requested})
	}
	c.backfillMu.Unlock()

	for _, r := range resolved {
		if !r.exhausted {
			continue
		}
		c.log.Infof("On-demand history for %s returned less than requested; marking history exhausted", r.chatID)
		chat, changed, err := c.store.UpdateChatHistoryExhausted(ctx, r.chatID, true)
		if err != nil {
			c.log.Warnf("Failed to mark history exhausted for %s: %v", r.chatID, err)
			continue
		}
		if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
}
