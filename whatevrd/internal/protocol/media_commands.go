package protocol

import (
	"context"
	"log"
	"strings"

	"whatevrd/internal/app"
)

func (h commandHandlers) mediaDownload(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	// media.download is ack-then-lifecycle (PROTOCOL.md): the response is {} and
	// all progress/outcome is observable through the `transfers` view and the
	// message row (media.path on success, media.download_error on failure), which
	// DownloadMessageMedia already publishes. Run it in the background so the
	// command does not block on the full download; a detached context outlives the
	// request. The wa layer coalesces duplicate in-flight downloads of the same
	// message, so a repeat call is harmless.
	messageID := strings.TrimSpace(p.MessageID)
	go func() {
		if _, err := h.actions.DownloadMessageMedia(context.Background(), messageID); err != nil {
			log.Printf("protocol: media.download %s: %v", messageID, err)
		}
	}()
	return nil, nil
}

// mediaCancelDownload stops an in-flight fetch. Whatever has already landed on
// disk stays there, so asking again resumes rather than starting over.
func (h commandHandlers) mediaCancelDownload(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	if err := h.actions.CancelMessageMediaDownload(ctx, strings.TrimSpace(p.MessageID)); err != nil {
		if perr := mapCommandError(err); perr != nil {
			return nil, perr
		}
	}
	return nil, nil
}

type fetchProfilePictureParams struct {
	JID string `json:"jid"`
}

func (h commandHandlers) mediaFetchProfilePicture(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p fetchProfilePictureParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.JID) == "" {
		return nil, errorf(CodeInvalidParams, "jid is required")
	}
	path, err := h.actions.FetchProfilePicture(ctx, strings.TrimSpace(p.JID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"path": path}, nil
}

// mediaStream hands back a loopback URL the frontend's player can open while
// the bytes are still arriving. Unlike media.download it is a query, not a
// lifecycle: the daemon fetches ranges on demand behind the URL, and the
// message row still upserts with `media.path` once the whole file has landed
// and verified, after which the frontend should use the path instead.
func (h commandHandlers) mediaStreamCommand(c *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if err := p.valid(); err != nil {
		return nil, err
	}
	messageID := strings.TrimSpace(p.MessageID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), netCommandTimeout)
		defer cancel()
		updates := make(chan app.MediaStreamUpdate, 1)
		stream, err := h.actions.StreamMessageMedia(ctx, messageID, func(update app.MediaStreamUpdate) {
			select {
			case updates <- update:
			default:
			}
		})
		if perr := mapCommandError(err); perr != nil {
			c.respondError(req.ID, perr, false)
			return
		}
		c.respondResult(req.ID, map[string]any{
			"stream_id":     stream.StreamID,
			"url":           stream.URL,
			"mime":          stream.Mime,
			"size_bytes":    stream.SizeBytes,
			"duration_secs": stream.DurationSecs,
		})

		select {
		case update := <-updates:
			if update.State != "" {
				c.enqueueMediaStreamUpdate(update)
			}
		case <-c.done:
		}
	}()
	return responded{}, nil
}
