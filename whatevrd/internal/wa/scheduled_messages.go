package wa

import (
	"context"
	"strconv"
	"time"

	appstore "whatevrd/internal/store"
)

func (c *Client) runScheduledMessages(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		c.enqueueDueScheduledMessages(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Client) enqueueDueScheduledMessages(ctx context.Context) {
	messages, err := c.store.DueScheduledMessages(ctx, time.Now(), 50)
	if err != nil {
		c.log.Warnf("list scheduled messages: %v", err)
		return
	}
	for _, scheduled := range messages {
		saved, err := c.store.SaveTextMessage(ctx, appstore.TextMessageInput{
			ID:     internalMessageIDForChat(scheduled.ChatID, "scheduled-"+strconv.FormatInt(scheduled.ID, 10)),
			ChatID: scheduled.ChatID, SenderID: "me", Text: scheduled.Text,
			Timestamp: time.Unix(scheduled.SendAt, 0), Direction: appstore.DirectionOutgoing,
			Status: appstore.StatusPending,
		})
		if err != nil {
			c.log.Warnf("enqueue scheduled message %d: %v", scheduled.ID, err)
			continue
		}
		if saved.Inserted {
			c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
		}
		if err := c.store.DeleteScheduledMessage(ctx, scheduled.ID); err != nil {
			c.log.Warnf("delete scheduled message %d: %v", scheduled.ID, err)
			continue
		}
		c.signalSendQueue()
	}
}
