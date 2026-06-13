package wa

import (
	"time"
)

// sendTiming tracks one outgoing message's progress through the send
// pipeline so the daemon log can show where the pending→sent latency
// actually goes (queue wait vs network ack vs status write).
type sendTiming struct {
	rpcArrival  time.Time // Client.SendText/SendMedia entry
	insertDone  time.Time // pending row committed
	queuePickup time.Time // sendPendingMessage entry
	ackReturn   time.Time // whatsmeow SendMessage returned OK
	statusWrite time.Time // UpdateMessageStatus committed
}

const slowSendTimelineThreshold = time.Second

func (c *Client) beginSendTiming(messageID string, rpcArrival time.Time) {
	c.sendTimingsMu.Lock()
	c.sendTimings[messageID] = &sendTiming{rpcArrival: rpcArrival, insertDone: time.Now()}
	c.sendTimingsMu.Unlock()
}

// markSendTiming mutates the timing entry for messageID if one exists.
// Messages drained from a previous daemon run have no entry; sendPendingMessage
// creates one then so the queue→publish stages are still measured.
func (c *Client) markSendTiming(messageID string, f func(*sendTiming)) {
	c.sendTimingsMu.Lock()
	t, ok := c.sendTimings[messageID]
	if !ok {
		t = &sendTiming{}
		c.sendTimings[messageID] = t
	}
	f(t)
	c.sendTimingsMu.Unlock()
}

// finishSendTiming removes and returns the timing entry, or nil.
func (c *Client) finishSendTiming(messageID string) *sendTiming {
	c.sendTimingsMu.Lock()
	t := c.sendTimings[messageID]
	delete(c.sendTimings, messageID)
	c.sendTimingsMu.Unlock()
	return t
}

// logSendTimeline emits a one-line stage breakdown for a sent message.
// Debug-level normally; Info when the total exceeds slowSendTimelineThreshold.
func (c *Client) logSendTimeline(messageID string, t *sendTiming) {
	if t == nil || t.queuePickup.IsZero() {
		return
	}
	published := time.Now()
	start := t.rpcArrival
	if start.IsZero() {
		// Drained from a previous run; only the queue stages are known.
		start = t.queuePickup
	}
	total := published.Sub(start)

	stage := func(from, to time.Time) time.Duration {
		if from.IsZero() || to.IsZero() {
			return 0
		}
		return to.Sub(from).Round(time.Millisecond)
	}
	logf := c.log.Debugf
	if total >= slowSendTimelineThreshold {
		logf = c.log.Infof
	}
	logf("send timeline %s: insert=%s queue-wait=%s send=%s status-write=%s total=%s",
		messageID,
		stage(t.rpcArrival, t.insertDone),
		stage(t.insertDone, t.queuePickup),
		stage(t.queuePickup, t.ackReturn),
		stage(t.ackReturn, t.statusWrite),
		total.Round(time.Millisecond),
	)
}
