package wa

import (
	"time"

	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
)

const offlineSyncProgressInterval = 250 * time.Millisecond

type offlineSyncSnapshot struct {
	active            bool
	totalEvents       uint32
	totalMessages     uint32
	processedEvents   uint32
	processedMessages uint32
	changedChats      map[string]uint32
}

func (c *Client) handleOfflineSyncPreview(evt *events.OfflineSyncPreview) {
	if evt == nil {
		return
	}
	totalEvents := nonNegativeUint32(evt.Total)
	totalMessages := nonNegativeUint32(evt.Messages)

	c.offlineSyncMu.Lock()
	c.offlineSyncActive = true
	c.offlineSyncTotalEvents = totalEvents
	c.offlineSyncTotalMessages = totalMessages
	c.offlineSyncProcessedEvents = 0
	c.offlineSyncProcessedMessages = 0
	c.offlineSyncChangedChats = make(map[string]uint32)
	c.offlineSyncLastPublish = time.Now()
	snapshot := c.offlineSyncSnapshotLocked()
	c.offlineSyncMu.Unlock()

	c.log.Infof("Offline sync started: %d messages, %d events", totalMessages, totalEvents)
	c.publishOfflineSyncProgress(snapshot, false)
}

func (c *Client) handleOfflineSyncCompleted(evt *events.OfflineSyncCompleted) {
	completedCount := uint32(0)
	if evt != nil {
		completedCount = nonNegativeUint32(evt.Count)
	}

	c.offlineSyncMu.Lock()
	if !c.offlineSyncActive && c.offlineSyncProcessedEvents == 0 && c.offlineSyncProcessedMessages == 0 {
		c.offlineSyncMu.Unlock()
		return
	}
	if completedCount > c.offlineSyncProcessedEvents {
		c.offlineSyncProcessedEvents = completedCount
	}
	c.offlineSyncActive = false
	snapshot := c.offlineSyncSnapshotLocked()
	c.offlineSyncChangedChats = make(map[string]uint32)
	c.offlineSyncMu.Unlock()

	c.publishOfflineSyncBackfilled(snapshot.changedChats)
	c.publishOfflineSyncProgress(snapshot, true)
	c.log.Infof("Offline sync completed: %d messages, %d events", snapshot.processedMessages, snapshot.processedEvents)
}

func (c *Client) offlineSyncInProgress() bool {
	c.offlineSyncMu.Lock()
	active := c.offlineSyncActive
	c.offlineSyncMu.Unlock()
	return active
}

func (c *Client) recordOfflineSyncEvent() {
	c.offlineSyncMu.Lock()
	if !c.offlineSyncActive {
		c.offlineSyncMu.Unlock()
		return
	}
	c.offlineSyncProcessedEvents++
	snapshot, publish := c.maybeOfflineSyncSnapshotLocked(false)
	c.offlineSyncMu.Unlock()

	if publish {
		c.publishOfflineSyncBackfilled(snapshot.changedChats)
		c.publishOfflineSyncProgress(snapshot, false)
	}
}

func (c *Client) recordOfflineSyncMessage(chatID string, inserted bool) {
	c.offlineSyncMu.Lock()
	if !c.offlineSyncActive {
		c.offlineSyncMu.Unlock()
		return
	}
	c.offlineSyncProcessedMessages++
	if inserted && chatID != "" {
		if c.offlineSyncChangedChats == nil {
			c.offlineSyncChangedChats = make(map[string]uint32)
		}
		c.offlineSyncChangedChats[chatID]++
	}
	snapshot, publish := c.maybeOfflineSyncSnapshotLocked(false)
	c.offlineSyncMu.Unlock()

	if publish {
		c.publishOfflineSyncBackfilled(snapshot.changedChats)
		c.publishOfflineSyncProgress(snapshot, false)
	}
}

func (c *Client) maybeOfflineSyncSnapshotLocked(force bool) (offlineSyncSnapshot, bool) {
	now := time.Now()
	if !force && now.Sub(c.offlineSyncLastPublish) < offlineSyncProgressInterval {
		return offlineSyncSnapshot{}, false
	}
	c.offlineSyncLastPublish = now
	snapshot := c.offlineSyncSnapshotLocked()
	c.offlineSyncChangedChats = make(map[string]uint32)
	return snapshot, true
}

func (c *Client) offlineSyncSnapshotLocked() offlineSyncSnapshot {
	changedChats := make(map[string]uint32, len(c.offlineSyncChangedChats))
	for chatID, count := range c.offlineSyncChangedChats {
		changedChats[chatID] = count
	}
	return offlineSyncSnapshot{
		active:            c.offlineSyncActive,
		totalEvents:       c.offlineSyncTotalEvents,
		totalMessages:     c.offlineSyncTotalMessages,
		processedEvents:   c.offlineSyncProcessedEvents,
		processedMessages: c.offlineSyncProcessedMessages,
		changedChats:      changedChats,
	}
}

func (c *Client) publishOfflineSyncProgress(snapshot offlineSyncSnapshot, complete bool) {
	phase := app.HistorySyncPhaseProcessing
	if complete {
		phase = app.HistorySyncPhaseComplete
	}
	c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:               app.HistorySyncTypeOfflineCatchup,
		ProgressPercent:        offlineSyncPercent(snapshot.processedEvents, snapshot.totalEvents, complete),
		ConversationsInChunk:   snapshot.totalEvents,
		MessagesInChunk:        snapshot.totalMessages,
		IsComplete:             complete,
		Phase:                  phase,
		ProcessedConversations: snapshot.processedEvents,
		ProcessedMessages:      snapshot.processedMessages,
	})
}

func (c *Client) publishOfflineSyncBackfilled(changedChats map[string]uint32) {
	for chatID, count := range changedChats {
		if chatID == "" || count == 0 {
			continue
		}
		c.daemon.PublishHistoryBackfilled(chatID, count)
	}
}

func offlineSyncPercent(processed, total uint32, complete bool) uint32 {
	if complete {
		return 100
	}
	if total == 0 {
		return 0
	}
	percent := uint32((uint64(processed) * 100) / uint64(total))
	if percent >= 100 {
		return 99
	}
	return percent
}

func nonNegativeUint32(value int) uint32 {
	if value <= 0 {
		return 0
	}
	return uint32(value)
}
