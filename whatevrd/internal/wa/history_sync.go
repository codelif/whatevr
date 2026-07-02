package wa

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func (c *Client) handleManualHistorySyncNotification(ctx context.Context, evt *events.Message) bool {
	if evt == nil || evt.Info.ID == "" {
		return false
	}
	protocol := evt.Message.GetProtocolMessage()
	if protocol == nil {
		protocol = evt.RawMessage.GetProtocolMessage()
	}
	notif := protocol.GetHistorySyncNotification()
	if notif == nil {
		return false
	}
	syncType := historySyncTypeFromNotification(notif.GetSyncType())

	chunk := historySyncChunkFromNotification(evt.Info.ID, notif)
	if _, err := c.store.SaveHistorySyncChunk(ctx, chunk); err != nil {
		c.log.Errorf("Failed to persist history sync notification %s: %v", evt.Info.ID, err)
		return true
	}
	c.publishHistorySyncChunkProgress(chunk, syncType, app.HistorySyncPhaseQueued)
	c.signalHistorySyncWorker()
	return true
}

func historySyncChunkFromNotification(id string, notif *waE2E.HistorySyncNotification) appstore.HistorySyncChunk {
	return appstore.HistorySyncChunk{
		ID:            id,
		SyncType:      int32(notif.GetSyncType()),
		ChunkOrder:    notif.GetChunkOrder(),
		Progress:      notif.GetProgress(),
		FileLength:    notif.GetFileLength(),
		DirectPath:    notif.GetDirectPath(),
		MediaKey:      notif.GetMediaKey(),
		FileSHA256:    notif.GetFileSHA256(),
		FileEncSHA256: notif.GetFileEncSHA256(),
		EncHandle:     notif.GetEncHandle(),
		InlinePayload: notif.GetInitialHistBootstrapInlinePayload(),
	}
}

func historySyncTypeFromNotification(t waE2E.HistorySyncType) app.HistorySyncType {
	switch t {
	case waE2E.HistorySyncType_INITIAL_BOOTSTRAP:
		return app.HistorySyncTypeInitialBootstrap
	case waE2E.HistorySyncType_INITIAL_STATUS_V3:
		return app.HistorySyncTypeInitialStatusV3
	case waE2E.HistorySyncType_FULL:
		return app.HistorySyncTypeFull
	case waE2E.HistorySyncType_RECENT:
		return app.HistorySyncTypeRecent
	case waE2E.HistorySyncType_PUSH_NAME:
		return app.HistorySyncTypePushName
	case waE2E.HistorySyncType_NON_BLOCKING_DATA:
		return app.HistorySyncTypeNonBlockingData
	case waE2E.HistorySyncType_ON_DEMAND:
		return app.HistorySyncTypeOnDemand
	default:
		return app.HistorySyncTypeUnspecified
	}
}

func historySyncNotificationFromChunk(chunk appstore.HistorySyncChunk) *waE2E.HistorySyncNotification {
	syncType := waE2E.HistorySyncType(chunk.SyncType)
	chunkOrder := chunk.ChunkOrder
	progress := chunk.Progress
	fileLength := chunk.FileLength
	notif := &waE2E.HistorySyncNotification{
		SyncType:      &syncType,
		ChunkOrder:    &chunkOrder,
		Progress:      &progress,
		FileLength:    &fileLength,
		MediaKey:      chunk.MediaKey,
		FileSHA256:    chunk.FileSHA256,
		FileEncSHA256: chunk.FileEncSHA256,
	}
	// Only set the inline payload when it actually carries bytes. An empty BLOB
	// scanned from SQLite comes back as a non-nil []byte{}, and whatmeow's
	// DownloadHistorySync treats any non-nil InitialHistBootstrapInlinePayload as
	// "data already inline", skipping the real media download and then failing in
	// zlib with "unexpected EOF". Leaving the field nil lets it fall through to
	// cli.Download for download-based chunks (RECENT, NON_BLOCKING_DATA, ...).
	if len(chunk.InlinePayload) > 0 {
		notif.InitialHistBootstrapInlinePayload = chunk.InlinePayload
	}
	if chunk.DirectPath != "" {
		notif.DirectPath = proto.String(chunk.DirectPath)
	}
	if chunk.EncHandle != "" {
		notif.EncHandle = proto.String(chunk.EncHandle)
	}
	return notif
}

func (c *Client) signalHistorySyncWorker() {
	c.historySyncMu.Lock()
	if c.historySyncRunning {
		c.historySyncWake = true
		c.historySyncMu.Unlock()
		return
	}
	c.historySyncRunning = true
	c.historySyncWake = false
	c.historySyncMu.Unlock()

	go c.runHistorySyncWorker(c.backgroundContext())
}

func (c *Client) runHistorySyncWorker(ctx context.Context) {
	defer func() {
		c.historySyncMu.Lock()
		restart := c.historySyncWake
		c.historySyncWake = false
		c.historySyncRunning = false
		c.historySyncMu.Unlock()
		if restart && ctx.Err() == nil {
			c.signalHistorySyncWorker()
		}
	}()

	if ctx.Err() != nil {
		return
	}
	for {
		chunks, err := c.store.ListRecoverableHistorySyncChunks(ctx, 100)
		if err != nil {
			c.log.Errorf("Failed to list recoverable history sync chunks: %v", err)
			return
		}
		if len(chunks) == 0 {
			return
		}
		for _, chunk := range chunks {
			if ctx.Err() != nil {
				return
			}
			if !c.processHistorySyncChunk(ctx, chunk) {
				return
			}
		}
	}
}

func (c *Client) processHistorySyncChunk(ctx context.Context, chunk appstore.HistorySyncChunk) bool {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return false
	}
	started := time.Now()
	syncType := historySyncTypeFromNotification(waE2E.HistorySyncType(chunk.SyncType))

	if chunk.Status != appstore.HistorySyncStatusProcessed {
		if err := c.store.MarkHistorySyncChunkProcessing(ctx, chunk.ID); err != nil {
			c.log.Errorf("Failed to mark history sync chunk %s processing: %v", chunk.ID, err)
			return false
		}
		c.publishHistorySyncChunkProgress(chunk, syncType, app.HistorySyncPhaseDownloading)
		downloadStarted := time.Now()
		blob, err := client.DownloadHistorySync(ctx, historySyncNotificationFromChunk(chunk), true)
		if err != nil {
			_ = c.store.MarkHistorySyncChunkFailed(ctx, chunk.ID, err.Error())
			_ = client.SendHistorySyncServerErrorReceipt(context.WithoutCancel(ctx), chunk.ID, chunk.MediaKey)
			c.log.Warnf("Failed to download history sync chunk %s: %v", chunk.ID, err)
			return false
		}
		c.log.Debugf("Downloaded history sync chunk %s (type %d, chunk %d, progress %d) in %s", chunk.ID, chunk.SyncType, chunk.ChunkOrder, chunk.Progress, time.Since(downloadStarted).Round(time.Millisecond))

		if err := client.SendProtocolMessageReceipt(ctx, chunk.ID, types.ReceiptTypeHistorySync); err != nil {
			c.log.Warnf("Failed to acknowledge history sync chunk %s: %v", chunk.ID, err)
			return false
		}
		c.log.Debugf("Acknowledged history sync chunk %s (type %d, chunk %d, progress %d) before ingestion", chunk.ID, chunk.SyncType, chunk.ChunkOrder, chunk.Progress)

		processStarted := time.Now()
		c.publishHistorySyncChunkProgress(chunk, syncType, app.HistorySyncPhaseProcessing)
		c.processHistorySyncData(ctx, blob)
		if ctx.Err() != nil {
			return false
		}
		c.log.Debugf("Processed history sync chunk %s (type %d, chunk %d, progress %d) in %s", chunk.ID, chunk.SyncType, chunk.ChunkOrder, chunk.Progress, time.Since(processStarted).Round(time.Millisecond))
		if err := c.store.MarkHistorySyncChunkProcessed(ctx, chunk.ID); err != nil {
			c.log.Errorf("Failed to mark history sync chunk %s processed: %v", chunk.ID, err)
			return false
		}
		// Each chunk brings LID→PN mappings with it; retry any pin/archive/mute
		// state that was parked because its mapping hadn't landed yet.
		c.reconcilePendingAppState(ctx, false)
	} else {
		if err := client.SendProtocolMessageReceipt(ctx, chunk.ID, types.ReceiptTypeHistorySync); err != nil {
			c.log.Warnf("Failed to acknowledge processed history sync chunk %s: %v", chunk.ID, err)
			return false
		}
		c.log.Debugf("Acknowledged processed history sync chunk %s (type %d, chunk %d, progress %d)", chunk.ID, chunk.SyncType, chunk.ChunkOrder, chunk.Progress)
	}
	if err := c.store.MarkHistorySyncChunkAcked(ctx, chunk.ID); err != nil {
		c.log.Errorf("Failed to mark history sync chunk %s acked: %v", chunk.ID, err)
		return false
	}
	if err := c.store.PruneAckedHistorySyncChunks(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
		c.log.Warnf("Failed to prune acked history sync chunks: %v", err)
	}
	c.log.Debugf("Finished history sync chunk %s (type %d, chunk %d, progress %d) in %s", chunk.ID, chunk.SyncType, chunk.ChunkOrder, chunk.Progress, time.Since(started).Round(time.Millisecond))
	if chunk.DirectPath != "" {
		go c.deleteHistorySyncMedia(context.WithoutCancel(ctx), client, chunk)
	}
	return true
}

func (c *Client) publishHistorySyncChunkProgress(chunk appstore.HistorySyncChunk, syncType app.HistorySyncType, phase app.HistorySyncPhase) {
	evt := app.HistorySyncEvent{
		SyncType:        syncType,
		ProgressPercent: chunk.Progress,
		ChunkOrder:      chunk.ChunkOrder,
		Phase:           phase,
	}
	c.noteHistorySyncActivity(evt)
	c.daemon.PublishHistorySyncProgress(evt)
}

// The initial sync is phone-driven: the phone pushes chunks and the daemon
// can only wait. When it goes quiet below 100% for this long, the sync is
// declared stalled so the settle work isn't held hostage and the UI can stop
// pretending progress is being made. A later chunk resumes everything.
const historySyncStallTimeout = 3 * time.Minute

// historySyncTypeTracksStall limits the stall watchdog to the phone-driven
// initial sync flow; on-demand responses have their own expiry (backfill.go)
// and auxiliary types complete instantly.
func historySyncTypeTracksStall(syncType app.HistorySyncType) bool {
	switch syncType {
	case app.HistorySyncTypeInitialBootstrap, app.HistorySyncTypeInitialStatusV3, app.HistorySyncTypeFull, app.HistorySyncTypeRecent:
		return true
	default:
		return false
	}
}

// noteHistorySyncActivity records sync progress for the stall watchdog and
// arms it if idle. Cheap to call often: the timer is only created once and
// re-arms itself from historySyncLastActivity.
func (c *Client) noteHistorySyncActivity(evt app.HistorySyncEvent) {
	if !historySyncTypeTracksStall(evt.SyncType) {
		return
	}
	c.historySyncMu.Lock()
	c.historySyncLastEvent = evt
	c.historySyncLastActivity = time.Now()
	if c.historySyncStallTimer == nil {
		c.historySyncStallTimer = time.AfterFunc(historySyncStallTimeout, c.historySyncStallCheck)
	}
	c.historySyncMu.Unlock()
}

func (c *Client) clearHistorySyncStallWatch() {
	c.historySyncMu.Lock()
	if c.historySyncStallTimer != nil {
		c.historySyncStallTimer.Stop()
		c.historySyncStallTimer = nil
	}
	c.historySyncMu.Unlock()
}

func (c *Client) historySyncStallCheck() {
	c.historySyncMu.Lock()
	idle := time.Since(c.historySyncLastActivity)
	if idle < historySyncStallTimeout {
		c.historySyncStallTimer = time.AfterFunc(historySyncStallTimeout-idle, c.historySyncStallCheck)
		c.historySyncMu.Unlock()
		return
	}
	c.historySyncStallTimer = nil
	last := c.historySyncLastEvent
	c.historySyncMu.Unlock()

	c.log.Warnf("History sync stalled: no activity for %s (type %d, chunk %d, progress %d%%); running settle work early", historySyncStallTimeout, last.SyncType, last.ChunkOrder, last.ProgressPercent)
	c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:        last.SyncType,
		ProgressPercent: last.ProgressPercent,
		ChunkOrder:      last.ChunkOrder,
		Phase:           app.HistorySyncPhaseStalled,
	})

	ctx := c.backgroundContext()
	c.reconcileAfterHistorySync(ctx)
	c.kickAvatarBackgroundRefresh()
}

func (c *Client) deleteHistorySyncMedia(ctx context.Context, client *whatsmeow.Client, chunk appstore.HistorySyncChunk) {
	if err := client.DeleteMedia(ctx, whatsmeow.MediaHistory, chunk.DirectPath, chunk.FileEncSHA256, chunk.EncHandle); err != nil {
		c.log.Warnf("Failed to delete history sync media for chunk %s: %v", chunk.ID, err)
	} else {
		c.log.Debugf("Deleted history sync media for chunk %s", chunk.ID)
	}
}

// historySyncTypeCarriesMessages reports whether a sync type ingests message
// history (as opposed to push names or non-blocking data); its completion is
// what marks the initial sync as settled.
func historySyncTypeCarriesMessages(syncType app.HistorySyncType) bool {
	switch syncType {
	case app.HistorySyncTypeInitialBootstrap, app.HistorySyncTypeInitialStatusV3, app.HistorySyncTypeFull, app.HistorySyncTypeRecent, app.HistorySyncTypeOnDemand:
		return true
	default:
		return false
	}
}
