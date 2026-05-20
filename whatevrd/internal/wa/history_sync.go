package wa

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

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

	chunk := historySyncChunkFromNotification(evt.Info.ID, notif)
	if _, err := c.store.SaveHistorySyncChunk(ctx, chunk); err != nil {
		c.log.Errorf("Failed to persist history sync notification %s: %v", evt.Info.ID, err)
		return true
	}
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
	chunks, err := c.store.ListRecoverableHistorySyncChunks(ctx, 100)
	if err != nil {
		c.log.Errorf("Failed to list recoverable history sync chunks: %v", err)
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

func (c *Client) processHistorySyncChunk(ctx context.Context, chunk appstore.HistorySyncChunk) bool {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return false
	}

	if chunk.Status != appstore.HistorySyncStatusProcessed {
		if err := c.store.MarkHistorySyncChunkProcessing(ctx, chunk.ID); err != nil {
			c.log.Errorf("Failed to mark history sync chunk %s processing: %v", chunk.ID, err)
			return false
		}
		blob, err := client.DownloadHistorySync(ctx, historySyncNotificationFromChunk(chunk), true)
		if err != nil {
			_ = c.store.MarkHistorySyncChunkFailed(ctx, chunk.ID, err.Error())
			_ = client.SendHistorySyncServerErrorReceipt(context.WithoutCancel(ctx), chunk.ID, chunk.MediaKey)
			c.log.Warnf("Failed to download history sync chunk %s: %v", chunk.ID, err)
			return false
		}
		c.processHistorySyncData(ctx, blob)
		if ctx.Err() != nil {
			return false
		}
		if err := c.store.MarkHistorySyncChunkProcessed(ctx, chunk.ID); err != nil {
			c.log.Errorf("Failed to mark history sync chunk %s processed: %v", chunk.ID, err)
			return false
		}
	}

	if err := client.SendProtocolMessageReceipt(ctx, chunk.ID, types.ReceiptTypeHistorySync); err != nil {
		c.log.Warnf("Failed to acknowledge history sync chunk %s: %v", chunk.ID, err)
		return false
	}
	if err := c.store.MarkHistorySyncChunkAcked(ctx, chunk.ID); err != nil {
		c.log.Errorf("Failed to mark history sync chunk %s acked: %v", chunk.ID, err)
		return false
	}
	c.log.Debugf("Acknowledged history sync chunk %s (type %d, chunk %d, progress %d)", chunk.ID, chunk.SyncType, chunk.ChunkOrder, chunk.Progress)
	if c.daemon == nil || !c.daemon.HasActiveHistorySync() {
		c.scheduleAvatarRefresh(ctx, 2*time.Second)
	}
	if chunk.DirectPath != "" {
		if err := client.DeleteMedia(context.WithoutCancel(ctx), whatsmeow.MediaHistory, chunk.DirectPath, chunk.FileEncSHA256, chunk.EncHandle); err != nil {
			c.log.Warnf("Failed to delete history sync media for chunk %s: %v", chunk.ID, err)
		} else {
			c.log.Debugf("Deleted history sync media for chunk %s", chunk.ID)
		}
	}
	return true
}
