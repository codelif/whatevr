package protocol

import (
	"context"
	"log"
	"strings"
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
