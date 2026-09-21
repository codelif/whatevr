package protocol

import (
	"context"
	"log"
	"strings"
	"unicode/utf8"
)

// status.mark_viewed flags a status as seen locally. Viewed receipts to the
// sender are not sent yet; this only drives the local ring/badge state.
func (h commandHandlers) statusMarkViewed(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p statusIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.StatusID) == "" {
		return nil, errorf(CodeInvalidParams, "status_id is required")
	}
	_, err := h.actions.MarkStatusViewed(ctx, strings.TrimSpace(p.StatusID))
	return nil, mapCommandError(err)
}

type statusIDParams struct {
	StatusID string `json:"status_id"`
}

// status.keep_sender pins (or unpins) a contact's expired statuses: kept
// senders grow an archived section in the Status tab instead of having their
// older statuses hidden once past 24h. Synchronous: a local flag flip.
func (h commandHandlers) statusKeepSender(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p struct {
		SenderID string `json:"sender_id"`
		Kept     bool   `json:"kept"`
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.SenderID) == "" {
		return nil, errorf(CodeInvalidParams, "sender_id is required")
	}
	err := h.actions.SetStatusKeepSender(context.Background(), strings.TrimSpace(p.SenderID), p.Kept)
	return nil, mapCommandError(err)
}

// status.mute_sender hides (or unhides) a contact's statuses: muted senders
// collect under the Status tab's Muted section instead of the main list.
// Synchronous: a local flag flip.
func (h commandHandlers) statusMuteSender(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p struct {
		SenderID string `json:"sender_id"`
		Muted    bool   `json:"muted"`
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.SenderID) == "" {
		return nil, errorf(CodeInvalidParams, "sender_id is required")
	}
	err := h.actions.SetStatusMutedSender(context.Background(), strings.TrimSpace(p.SenderID), p.Muted)
	return nil, mapCommandError(err)
}

// status.viewers lists who viewed one of our statuses, most recent first,
// with display names resolved. Only our own statuses ever have viewers.
func (h commandHandlers) statusViewers(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p statusIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.StatusID) == "" {
		return nil, errorf(CodeInvalidParams, "status_id is required")
	}
	viewers, err := h.actions.ListStatusViewers(ctx, strings.TrimSpace(p.StatusID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	out := make([]map[string]any, 0, len(viewers))
	for _, viewer := range viewers {
		out = append(out, map[string]any{
			"jid":       viewer.ViewerJID,
			"viewed_at": viewer.ViewedAt,
		})
	}
	return map[string]any{"viewers": out}, nil
}

// status.download is ack-then-lifecycle like media.download: the response is
// {} and the outcome is observable through the `status` view (media.path on
// success). Runs in the background so the command does not block on the
// fetch.
func (h commandHandlers) statusDownload(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p statusIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.StatusID) == "" {
		return nil, errorf(CodeInvalidParams, "status_id is required")
	}
	statusID := strings.TrimSpace(p.StatusID)
	go func() {
		if _, err := h.actions.DownloadStatusMedia(context.Background(), statusID); err != nil {
			log.Printf("protocol: status.download %s: %v", statusID, err)
		}
	}()
	return nil, nil
}

type statusPostParams struct {
	Text    string `json:"text"`
	Path    string `json:"path"`
	Caption string `json:"caption"`
	// Background is the text-status backdrop as 0xRRGGBB (0 = default green);
	// Font is the WhatsApp font id (0 = system default).
	Background uint32 `json:"background"`
	Font       int32  `json:"font"`
}

// status.post publishes a text or media status. Exactly one of text or path
// must be set; the response carries the stored status id for correlation, and
// the status itself arrives through the `status` view like anyone else's.
// Text posts as a styled ExtendedTextMessage (background + font): a bare
// Conversation renders as "unsupported" on official clients.
func (h commandHandlers) statusPost(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p statusPostParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(p.Text) > maxCommandTextRunes {
		return nil, errorf(CodeInvalidParams, "text must be <= %d characters", maxCommandTextRunes)
	}
	if utf8.RuneCountInString(p.Caption) > maxCommandCaptionRunes {
		return nil, errorf(CodeInvalidParams, "caption must be <= %d characters", maxCommandCaptionRunes)
	}
	posted, err := h.actions.PostStatus(ctx, p.Text, strings.TrimSpace(p.Path), p.Caption, p.Background, p.Font)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"status_id": posted.ID}, nil
}

type statusReplyParams struct {
	StatusID string `json:"status_id"`
	Text     string `json:"text"`
}

// status.reply sends a DM to the status author quoting their status, the way
// official clients reply to stories.
func (h commandHandlers) statusReply(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p statusReplyParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.StatusID) == "" {
		return nil, errorf(CodeInvalidParams, "status_id is required")
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, errorf(CodeInvalidParams, "text is required")
	}
	if utf8.RuneCountInString(p.Text) > maxCommandTextRunes {
		return nil, errorf(CodeInvalidParams, "text must be <= %d characters", maxCommandTextRunes)
	}
	saved, err := h.actions.ReplyToStatus(ctx, strings.TrimSpace(p.StatusID), p.Text)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

// status.delete revokes one of our own statuses and drops its local row.
func (h commandHandlers) statusDelete(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p statusIDParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.StatusID) == "" {
		return nil, errorf(CodeInvalidParams, "status_id is required")
	}
	return nil, mapCommandError(h.actions.DeleteStatus(ctx, strings.TrimSpace(p.StatusID)))
}
