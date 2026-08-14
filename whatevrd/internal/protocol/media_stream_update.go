package protocol

import (
	"encoding/json"

	"whatevrd/internal/app"
)

type mediaStreamUpdateEvent struct {
	Event     string `json:"event"`
	StreamID  string `json:"stream_id"`
	MessageID string `json:"message_id"`
	State     string `json:"state"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (c *conn) enqueueMediaStreamUpdate(update app.MediaStreamUpdate) {
	if update.StreamID == "" || update.MessageID == "" {
		return
	}
	line, err := json.Marshal(mediaStreamUpdateEvent{
		Event:     "media_stream_update",
		StreamID:  update.StreamID,
		MessageID: update.MessageID,
		State:     update.State,
		Path:      update.Path,
		Error:     update.ErrorText,
	})
	if err != nil {
		return
	}
	c.q.push(line, false)
}
