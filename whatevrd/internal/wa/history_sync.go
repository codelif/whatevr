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
	c.markHistorySyncAvatarDeferralActive(syncType)

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
		SyncType:                          &syncType,
		ChunkOrder:                        &chunkOrder,
		Progress:                          &progress,
		FileLength:                        &fileLength,
		MediaKey:                          chunk.MediaKey,
		FileSHA256:                        chunk.FileSHA256,
		FileEncSHA256:                     chunk.FileEncSHA256,
		InitialHistBootstrapInlinePayload: chunk.InlinePayload,
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
	c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:        syncType,
		ProgressPercent: chunk.Progress,
		ChunkOrder:      chunk.ChunkOrder,
		Phase:           phase,
	})
}

func (c *Client) deleteHistorySyncMedia(ctx context.Context, client *whatsmeow.Client, chunk appstore.HistorySyncChunk) {
	if err := client.DeleteMedia(ctx, whatsmeow.MediaHistory, chunk.DirectPath, chunk.FileEncSHA256, chunk.EncHandle); err != nil {
		c.log.Warnf("Failed to delete history sync media for chunk %s: %v", chunk.ID, err)
	} else {
		c.log.Debugf("Deleted history sync media for chunk %s", chunk.ID)
	}
}

func (c *Client) markHistorySyncAvatarDeferralActive(syncType app.HistorySyncType) {
	if !historySyncBlocksAvatarFetch(syncType) {
		return
	}
	c.historySyncMu.Lock()
	c.historySyncLastActivity = time.Now()
	c.historySyncActive = true
	if c.historySyncIdleTimer != nil {
		c.historySyncIdleTimer.Stop()
	}
	c.historySyncIdleTimer = time.AfterFunc(historySyncAvatarDeferralIdle, c.expireHistorySyncAvatarDeferral)
	c.historySyncMu.Unlock()
}

func (c *Client) finishHistorySyncAvatarDeferral(syncType app.HistorySyncType, progress uint32) {
	if !historySyncBlocksAvatarFetch(syncType) {
		return
	}
	c.historySyncMu.Lock()
	c.historySyncActive = false
	if c.historySyncIdleTimer != nil {
		c.historySyncIdleTimer.Stop()
		c.historySyncIdleTimer = nil
	}
	c.historySyncMu.Unlock()
	c.log.Debugf("History sync complete for avatar deferral (type %d, progress %d); starting profile picture sync", syncType, progress)
	c.startProfilePictureSync(c.backgroundContext())
}

func (c *Client) expireHistorySyncAvatarDeferral() {
	c.historySyncMu.Lock()
	if !c.historySyncActive || time.Since(c.historySyncLastActivity) < historySyncAvatarDeferralIdle {
		c.historySyncMu.Unlock()
		return
	}
	c.historySyncActive = false
	c.historySyncIdleTimer = nil
	c.historySyncMu.Unlock()
	c.log.Debugf("History sync idle for %s; starting profile picture sync", historySyncAvatarDeferralIdle)
	c.startProfilePictureSync(c.backgroundContext())
}

func (c *Client) historySyncBlocksAvatarFetch() bool {
	c.historySyncMu.Lock()
	defer c.historySyncMu.Unlock()
	return c.historySyncActive || c.profilePictureSyncActive
}

func historySyncBlocksAvatarFetch(syncType app.HistorySyncType) bool {
	switch syncType {
	case app.HistorySyncTypeInitialBootstrap, app.HistorySyncTypeInitialStatusV3, app.HistorySyncTypeFull, app.HistorySyncTypeRecent, app.HistorySyncTypeOnDemand:
		return true
	default:
		return false
	}
}
