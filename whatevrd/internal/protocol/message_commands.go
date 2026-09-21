package protocol

import (
	"context"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"whatevrd/internal/app"
)

type scheduleTextParams struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
	SendAt int64  `json:"send_at"`
}

func (h commandHandlers) scheduleText(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p scheduleTextParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" || strings.TrimSpace(p.Text) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id and text are required")
	}
	if utf8.RuneCountInString(p.Text) > maxCommandTextRunes {
		return nil, errorf(CodeInvalidParams, "text must be <= %d characters", maxCommandTextRunes)
	}
	if p.SendAt <= 0 {
		return nil, errorf(CodeInvalidParams, "send_at must be a Unix timestamp")
	}
	id, err := h.actions.ScheduleText(context.Background(), strings.TrimSpace(p.ChatID), p.Text, time.Unix(p.SendAt, 0))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"scheduled_id": id}, nil
}

type scheduleListParams struct {
	ChatID string `json:"chat_id"`
}

func (h commandHandlers) scheduleList(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p scheduleListParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	messages, err := h.actions.ListScheduledMessages(context.Background(), strings.TrimSpace(p.ChatID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	items := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		items = append(items, map[string]any{
			"id":      m.ID,
			"chat_id": m.ChatID,
			"text":    m.Text,
			"send_at": m.SendAt,
		})
	}
	return map[string]any{"messages": items}, nil
}

type scheduleCancelParams struct {
	ID int64 `json:"id"`
}

func (h commandHandlers) scheduleCancel(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p scheduleCancelParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, errorf(CodeInvalidParams, "id is required")
	}
	return nil, mapCommandError(h.actions.CancelScheduledMessage(context.Background(), p.ID))
}

type sendTextParams struct {
	ChatID   string   `json:"chat_id"`
	Text     string   `json:"text"`
	ReplyTo  string   `json:"reply_to"`
	Mentions []string `json:"mentions"`
}

func (h commandHandlers) sendText(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendTextParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	// Validate against the trimmed text (reject whitespace-only) but send the
	// original: leading/trailing whitespace is user-authored content, not ours to
	// strip.
	if strings.TrimSpace(p.Text) == "" {
		return nil, errorf(CodeInvalidParams, "text is required")
	}
	if utf8.RuneCountInString(p.Text) > maxCommandTextRunes {
		return nil, errorf(CodeInvalidParams, "text must be <= %d characters", maxCommandTextRunes)
	}
	saved, err := h.actions.SendText(context.Background(), strings.TrimSpace(p.ChatID), p.Text, strings.TrimSpace(p.ReplyTo), trimStringSlice(p.Mentions))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type sendMediaParams struct {
	ChatID   string   `json:"chat_id"`
	Path     string   `json:"path"`
	Caption  string   `json:"caption"`
	ReplyTo  string   `json:"reply_to"`
	Mentions []string `json:"mentions"`
	// Kind forces the media kind: "image", "video", "audio", "voice" or
	// "document". Empty (or "auto") classifies from the file contents.
	Kind string `json:"kind"`
	// ViewOnce sends photo/video/audio media view-once.
	ViewOnce bool `json:"view_once"`
	// Filename overrides the document display name.
	Filename string `json:"filename"`
	// Quality is "standard" (photos downscaled to 1600px, like official
	// clients) or "hd" (original bytes). Empty means standard.
	Quality string `json:"quality"`
}

func (h commandHandlers) sendMedia(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendMediaParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	path := strings.TrimSpace(p.Path)
	if path == "" {
		return nil, errorf(CodeInvalidParams, "path is required")
	}
	// Caption is optional and user-authored; validate length but send it verbatim
	// (an intentional leading space or trailing newline is not ours to strip).
	if utf8.RuneCountInString(p.Caption) > maxCommandCaptionRunes {
		return nil, errorf(CodeInvalidParams, "caption must be <= %d characters", maxCommandCaptionRunes)
	}
	saved, err := h.actions.SendMediaWithOptions(context.Background(), strings.TrimSpace(p.ChatID), path, p.Caption, strings.TrimSpace(p.ReplyTo), trimStringSlice(p.Mentions), mediaSendOptions(p))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type sendMediaBatchParams struct {
	ChatID   string `json:"chat_id"`
	ReplyTo  string `json:"reply_to"`
	Kind     string `json:"kind"`
	ViewOnce bool   `json:"view_once"`
	Files    []struct {
		Path    string `json:"path"`
		Caption string `json:"caption"`
	} `json:"files"`
}

// send.media_batch sends several files as individual messages through the
// daemon's serialized send path. The frontend can only hold one in-flight
// send, so looping send.media client-side drops every file after the first.
// Per-file failures come back as {index, error} entries without stopping the
// rest.
func (h commandHandlers) sendMediaBatch(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendMediaBatchParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if len(p.Files) == 0 || len(p.Files) > 30 {
		return nil, errorf(CodeInvalidParams, "files must hold 1-30 entries")
	}
	files := make([]app.MediaBatchFile, 0, len(p.Files))
	for _, f := range p.Files {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			return nil, errorf(CodeInvalidParams, "every file needs a path")
		}
		if utf8.RuneCountInString(f.Caption) > maxCommandCaptionRunes {
			return nil, errorf(CodeInvalidParams, "caption must be <= %d characters", maxCommandCaptionRunes)
		}
		files = append(files, app.MediaBatchFile{Path: path, Caption: f.Caption})
	}
	opts := app.MediaSendOptions{
		Kind:     strings.TrimSpace(p.Kind),
		ViewOnce: p.ViewOnce,
	}
	saved, failed := h.actions.SendMediaBatch(context.Background(), strings.TrimSpace(p.ChatID), files, strings.TrimSpace(p.ReplyTo), opts)
	ids := make([]string, 0, len(saved))
	for _, s := range saved {
		ids = append(ids, s.Message.ID)
	}
	out := make([]map[string]any, 0, len(failed))
	for _, f := range failed {
		out = append(out, map[string]any{"index": f.Index, "error": f.Message})
	}
	return map[string]any{"message_ids": ids, "errors": out}, nil
}

// mediaSendOptions converts send.media params to the daemon's media options.
// It lives in this file (rather than inline) so the mapping is unit-testable.
func mediaSendOptions(p sendMediaParams) app.MediaSendOptions {
	return app.MediaSendOptions{
		Kind:     strings.TrimSpace(p.Kind),
		ViewOnce: p.ViewOnce,
		Filename: strings.TrimSpace(p.Filename),
		Quality:  strings.TrimSpace(p.Quality),
	}
}

type sendPollParams struct {
	ChatID   string   `json:"chat_id"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Multi    bool     `json:"multi"`
}

// send.poll creates a single- or multi-select poll (2–12 options).
func (h commandHandlers) sendPoll(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendPollParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.Question) == "" {
		return nil, errorf(CodeInvalidParams, "question is required")
	}
	if utf8.RuneCountInString(p.Question) > maxCommandCaptionRunes {
		return nil, errorf(CodeInvalidParams, "question must be <= %d characters", maxCommandCaptionRunes)
	}
	options := trimStringSlice(p.Options)
	if len(options) < 2 {
		return nil, errorf(CodeInvalidParams, "at least two options are required")
	}
	if len(options) > 12 {
		return nil, errorf(CodeInvalidParams, "at most 12 options per poll")
	}
	saved, err := h.actions.SendPoll(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.Question), options, p.Multi)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type sendContactParams struct {
	ChatID string `json:"chat_id"`
	Name   string `json:"name"`
	Phone  string `json:"phone"`
}

// send.contact shares a contact card (name + phone) as a vCard message.
func (h commandHandlers) sendContact(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendContactParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errorf(CodeInvalidParams, "name is required")
	}
	if strings.TrimSpace(p.Phone) == "" {
		return nil, errorf(CodeInvalidParams, "phone is required")
	}
	saved, err := h.actions.SendContact(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.Name), strings.TrimSpace(p.Phone))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type sendLocationParams struct {
	ChatID  string  `json:"chat_id"`
	Lat     float64 `json:"lat"`
	Long    float64 `json:"long"`
	Name    string  `json:"name"`
	Address string  `json:"address"`
}

// send.location shares a location pin.
func (h commandHandlers) sendLocation(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendLocationParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if p.Lat < -90 || p.Lat > 90 || p.Long < -180 || p.Long > 180 {
		return nil, errorf(CodeInvalidParams, "coordinates out of range")
	}
	saved, err := h.actions.SendLocation(ctx, strings.TrimSpace(p.ChatID), p.Lat, p.Long, strings.TrimSpace(p.Name), strings.TrimSpace(p.Address))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type sendStickerParams struct {
	ChatID   string `json:"chat_id"`
	CacheKey string `json:"cache_key"`
	ReplyTo  string `json:"reply_to"`
}

func (h commandHandlers) sendSticker(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p sendStickerParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	if strings.TrimSpace(p.CacheKey) == "" {
		return nil, errorf(CodeInvalidParams, "cache_key is required")
	}
	saved, err := h.actions.SendSticker(ctx, strings.TrimSpace(p.ChatID), strings.TrimSpace(p.CacheKey), strings.TrimSpace(p.ReplyTo))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"message_id": saved.Message.ID}, nil
}

type messageIDParams struct {
	MessageID string `json:"message_id"`
}

func (p messageIDParams) valid() *Error {
	if strings.TrimSpace(p.MessageID) == "" {
		return errorf(CodeInvalidParams, "message_id is required")
	}
	return nil
}

type messageReactParams struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

func (h commandHandlers) messageReact(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageReactParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	_, err := h.actions.SendReaction(ctx, strings.TrimSpace(p.MessageID), strings.TrimSpace(p.Emoji))
	return nil, mapCommandError(err)
}

type messageEditParams struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

// message.edit_history returns a message's superseded bodies, oldest first.
// The live row holds the current version; the frontend appends it as such.
// Synchronous: a local index read, no network.
func (h commandHandlers) messageEditHistory(_ *conn, req request) (any, *Error) {
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
	edits, err := h.actions.ListMessageEdits(context.Background(), strings.TrimSpace(p.MessageID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	out := make([]map[string]any, 0, len(edits))
	for _, e := range edits {
		out = append(out, map[string]any{
			"text":      e.Text,
			"edited_at": e.EditedAtMillis,
		})
	}
	return map[string]any{"edits": out}, nil
}

func (h commandHandlers) messageEdit(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageEditParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	// As with send.text: validate trimmed, edit with the original text.
	if strings.TrimSpace(p.Text) == "" {
		return nil, errorf(CodeInvalidParams, "text is required")
	}
	if utf8.RuneCountInString(p.Text) > maxCommandTextRunes {
		return nil, errorf(CodeInvalidParams, "text must be <= %d characters", maxCommandTextRunes)
	}
	_, err := h.actions.EditMessage(ctx, strings.TrimSpace(p.MessageID), p.Text)
	return nil, mapCommandError(err)
}

func (h commandHandlers) messageRevoke(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	_, err := h.actions.RevokeMessage(ctx, strings.TrimSpace(p.MessageID))
	return nil, mapCommandError(err)
}

func (h commandHandlers) messageDelete(_ *conn, req request) (any, *Error) {
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
	return nil, mapCommandError(h.actions.DeleteMessageForMe(context.Background(), strings.TrimSpace(p.MessageID)))
}

type messageStarParams struct {
	MessageID string `json:"message_id"`
	Starred   *bool  `json:"starred"`
}

func (h commandHandlers) messageStar(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageStarParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	if p.Starred == nil {
		return nil, errorf(CodeInvalidParams, "starred is required")
	}
	_, err := h.actions.SetMessageStarred(ctx, strings.TrimSpace(p.MessageID), *p.Starred)
	return nil, mapCommandError(err)
}

type messagePinParams struct {
	MessageID    string `json:"message_id"`
	Pinned       *bool  `json:"pinned"`
	DurationSecs int64  `json:"duration_secs"`
}

func (h commandHandlers) messagePin(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messagePinParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	if p.Pinned == nil {
		return nil, errorf(CodeInvalidParams, "pinned is required")
	}
	if p.DurationSecs < 0 || p.DurationSecs > math.MaxUint32 {
		return nil, errorf(CodeInvalidParams, "duration_secs must fit uint32")
	}
	_, err := h.actions.PinMessage(ctx, strings.TrimSpace(p.MessageID), *p.Pinned, uint32(p.DurationSecs))
	return nil, mapCommandError(err)
}

type messageForwardParams struct {
	MessageID string   `json:"message_id"`
	ChatIDs   []string `json:"chat_ids"`
}

func (h commandHandlers) messageForward(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p messageForwardParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	targets := uniqueTrimmedStrings(p.ChatIDs)
	if len(targets) == 0 {
		return nil, errorf(CodeInvalidParams, "at least one chat_id is required")
	}
	if len(targets) > maxCommandForwardTargets {
		return nil, errorf(CodeInvalidParams, "at most %d target chats per forward", maxCommandForwardTargets)
	}
	saved, err := h.actions.ForwardMessage(context.Background(), strings.TrimSpace(p.MessageID), targets)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	ids := make([]string, 0, len(saved))
	for _, result := range saved {
		ids = append(ids, result.Message.ID)
	}
	return map[string]any{"message_ids": ids}, nil
}

// messageMarkPlayed reports that the user listened to an inbound voice note.
// The visible effect is a played receipt on the sender's side and the row's
// `media.played` flag; the wa layer makes a repeat call a no-op.
func (h commandHandlers) messageMarkPlayed(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	return nil, mapCommandError(h.actions.MarkMessagePlayed(ctx, strings.TrimSpace(p.MessageID)))
}

// messageRequestFromPhone asks our own phone for a copy of a message this
// device could not decrypt. The visible effect is the waiting row updating with
// the request in flight; if the phone answers, the row turns into the message.
func (h commandHandlers) messageRequestFromPhone(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	return nil, mapCommandError(h.actions.RequestMessageFromPhone(ctx, strings.TrimSpace(p.MessageID)))
}

// pollVoteParams is a whole selection, not a delta: a voter takes a choice back
// by sending the selection without it, which is what the wire format means.
type pollVoteParams struct {
	MessageID string `json:"message_id"`
	OptionIDs []int  `json:"option_ids"`
}

// pollVote casts our own vote. The visible effect is the poll row upserting
// with the new tally; the command itself acks with nothing, as every command
// does.
func (h commandHandlers) pollVote(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p pollVoteParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	return nil, mapCommandError(h.actions.VotePoll(ctx, strings.TrimSpace(p.MessageID), p.OptionIDs))
}

// groupJoinInvite accepts an invitation. It answers with the chat to open,
// which is also the answer for a group we were already in: there the command
// joins nothing and simply says where to go.
func (h commandHandlers) groupJoinInvite(ctx context.Context, _ *conn, req request) (any, *Error) {
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
	chatID, err := h.actions.JoinGroupInvite(ctx, strings.TrimSpace(p.MessageID))
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{"chat_id": chatID}, nil
}

// eventRSVPParams is a whole answer, not a delta: answering again replaces what
// you said before, which is what the wire format means.
type eventRSVPParams struct {
	MessageID string `json:"message_id"`
	// Response is "going", "not_going" or "maybe".
	Response string `json:"response"`
	// ExtraGuests is how many people you are bringing, for an event whose
	// author allowed it. Ignored for one that did not.
	ExtraGuests int `json:"extra_guests"`
}

// eventRSVP answers a scheduled event. The visible effect is the event row
// upserting with the new attendee list; the command acks with nothing, as
// every command does.
func (h commandHandlers) eventRSVP(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p eventRSVPParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MessageID) == "" {
		return nil, errorf(CodeInvalidParams, "message_id is required")
	}
	if strings.TrimSpace(p.Response) == "" {
		return nil, errorf(CodeInvalidParams, "response is required")
	}
	return nil, mapCommandError(h.actions.RespondToEvent(ctx,
		strings.TrimSpace(p.MessageID), strings.TrimSpace(p.Response), p.ExtraGuests))
}
