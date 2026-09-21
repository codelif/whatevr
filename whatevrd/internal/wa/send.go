package wa

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

const (
	maxOutboundMediaBytes = 25 * 1024 * 1024
	// Documents sent via "send as document" carry their own cap: WhatsApp
	// accepts large documents (up to GBs), and neither size nor duration is
	// validated for them the way media kinds are. 100 MiB bounds the daemon's
	// whole-file buffering while covering real document use.
	maxOutboundDocumentBytes = 100 * 1024 * 1024
	maxPinnedChats           = 3
)

type readBatch struct {
	sender     types.JID
	messageIDs []types.MessageID
}

func (c *Client) SendText(ctx context.Context, chatID, text, replyToMessageID string, mentionedJIDs []string) (appstore.SavedTextMessage, error) {
	rpcArrival := time.Now()
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "text is required")
	}

	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()
	replyTo, err := c.replySnapshotForSend(ctx, chatID, replyToMessageID)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	messageID := client.GenerateMessageID()
	saved, err := c.store.SaveTextMessage(ctx, appstore.TextMessageInput{
		ID:          internalMessageIDForChat(chatID, messageID),
		ChatID:      chatID,
		ChatName:    "",
		SenderID:    "me",
		Text:        trimmedText,
		Timestamp:   time.Now(),
		Direction:   appstore.DirectionOutgoing,
		Status:      appstore.StatusPending,
		IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
		CountUnread: false,
		ReplyTo:     replyTo,
		Mentions:    c.resolveMentions(ctx, mentionedJIDs),
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	if saved.Inserted {
		c.beginSendTiming(saved.Message.ID, rpcArrival)
		c.log.Infof("Queued text message %s to %s", saved.Message.ID, chatID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID}, avatarPriorityVisible)
	c.signalSendQueue()
	return saved, nil
}

func (c *Client) ScheduleText(ctx context.Context, chatID, text string, sendAt time.Time) (int64, error) {
	if _, err := types.ParseJID(chatID); err != nil {
		return 0, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		return 0, app.NewCommandError(app.CommandErrorInvalidArgument, "text is required")
	}
	return c.store.ScheduleText(ctx, chatID, text, sendAt)
}

func (c *Client) ListScheduledMessages(ctx context.Context, chatID string) ([]appstore.ScheduledMessage, error) {
	return c.store.ListScheduledMessages(ctx, chatID, 200)
}

func (c *Client) CancelScheduledMessage(ctx context.Context, id int64) error {
	return c.store.DeleteScheduledMessage(ctx, id)
}

func (c *Client) SetChatPresence(ctx context.Context, chatID string, composing bool) error {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil
	}

	// Typing indicators are passive: with the toggle off the client simply
	// never announces composing (indistinguishable from an idle client).
	if composing && !c.appPreferences().SendTypingIndicators {
		return nil
	}

	jid, err := types.ParseJID(chatID)
	if err != nil {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}

	state := types.ChatPresencePaused
	if composing {
		state = types.ChatPresenceComposing
	}

	return client.SendChatPresence(ctx, jid, state, types.ChatPresenceMediaText)
}

func (c *Client) SubscribeChatPresence(ctx context.Context, chatID string) error {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil
	}

	jid, err := types.ParseJID(chatID)
	if err != nil {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	if jid.Server == types.GroupServer {
		return nil
	}

	if err := client.SendPresence(ctx, types.PresenceAvailable); err != nil {
		return err
	}
	return client.SubscribePresence(ctx, jid)
}

func (c *Client) SendMedia(ctx context.Context, chatID, filePath, caption, replyToMessageID string) (appstore.SavedTextMessage, error) {
	return c.SendMediaWithMentions(ctx, chatID, filePath, caption, replyToMessageID, nil)
}

func (c *Client) SendMediaWithMentions(ctx context.Context, chatID, filePath, caption, replyToMessageID string, mentionedJIDs []string) (appstore.SavedTextMessage, error) {
	return c.SendMediaWithOptions(ctx, chatID, filePath, caption, replyToMessageID, mentionedJIDs, MediaSendOptions{})
}

// MediaSendOptions is an alias of app.MediaSendOptions for callers that
// already import wa; the canonical definition lives in app so the protocol
// layer can name it without importing the WhatsApp client.
type MediaSendOptions = app.MediaSendOptions

func (c *Client) SendMediaWithOptions(ctx context.Context, chatID, filePath, caption, replyToMessageID string, mentionedJIDs []string, opts MediaSendOptions) (appstore.SavedTextMessage, error) {
	rpcArrival := time.Now()
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	}

	data, mimeType, extension, mediaKind, fileName, err := readOutboundMedia(filePath, opts)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	if opts.ViewOnce && !viewOnceSendable(mediaKind) {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "view-once is only supported for photo, video and audio messages")
	}
	// Standard quality downscales photos the way official clients do; HD (or
	// any non-image kind) keeps the original bytes.
	var mediaWidth, mediaHeight int32
	if mediaKind == appstore.MediaKindImage {
		if quality := strings.ToLower(strings.TrimSpace(opts.Quality)); quality != "" && quality != "standard" && quality != "hd" {
			return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "quality must be standard or hd")
		} else if quality != "hd" {
			scaledData, scaledMime, scaledExt, scaledW, scaledH := downscaleImageForStandard(data, mimeType)
			data, mimeType = scaledData, scaledMime
			if scaledExt != "" {
				extension = scaledExt
			}
			mediaWidth, mediaHeight = scaledW, scaledH
		}
	}

	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()
	replyTo, err := c.replySnapshotForSend(ctx, chatID, replyToMessageID)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	messageID := client.GenerateMessageID()
	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", chatID)
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return appstore.SavedTextMessage{}, err
	}
	localPath := filepath.Join(mediaDir, fmt.Sprintf("%s%s", safeFilenamePart(string(messageID)), extension))
	if err := writeFileAtomic(localPath, data, 0o600); err != nil {
		return appstore.SavedTextMessage{}, err
	}

	// Standard-quality images already carry scaled dimensions from the
	// downscaler; HD keeps the original file, so read its config here.
	if mediaKind == appstore.MediaKindImage && mediaWidth == 0 {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			mediaWidth = int32(cfg.Width)
			mediaHeight = int32(cfg.Height)
		}
	}

	saved, err := c.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, messageID),
			ChatID:      chatID,
			SenderID:    "me",
			Text:        caption,
			Timestamp:   time.Now(),
			Direction:   appstore.DirectionOutgoing,
			Status:      appstore.StatusPending,
			IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
			CountUnread: false,
			ReplyTo:     replyTo,
			Mentions:    c.resolveMentions(ctx, mentionedJIDs),
		},
		MediaKind:      mediaKind,
		MediaMimeType:  mimeType,
		MediaLocalPath: localPath,
		MediaWidth:     mediaWidth,
		MediaHeight:    mediaHeight,
		MediaSizeBytes: int64(len(data)),
		MediaFileName:  fileName,
		IsViewOnce:     opts.ViewOnce,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	if saved.Inserted {
		c.beginSendTiming(saved.Message.ID, rpcArrival)
		c.log.Infof("Queued media message %s to %s", saved.Message.ID, chatID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID}, avatarPriorityVisible)
	c.signalSendQueue()
	return saved, nil
}

// SendMediaBatch sends several files as individual messages through the same
// path as single sends (the daemon's send queue serializes them). It exists
// because the frontend can only hold one in-flight send at a time; looping
// send.media client-side drops every file after the first. Per-file results
// come back in order; a failure records {index, error} and continues with the
// rest so one bad file does not eat the batch.
func (c *Client) SendMediaBatch(ctx context.Context, chatID string, files []app.MediaBatchFile, replyToMessageID string, opts MediaSendOptions) ([]appstore.SavedTextMessage, []app.MediaBatchError) {
	saved := make([]appstore.SavedTextMessage, 0, len(files))
	var failed []app.MediaBatchError
	for i, file := range files {
		one, err := c.SendMediaWithOptions(ctx, chatID, file.Path, file.Caption, replyToMessageID, nil, opts)
		if err != nil {
			failed = append(failed, app.MediaBatchError{Index: i, Message: err.Error()})
			continue
		}
		saved = append(saved, one)
	}
	return saved, failed
}

func (c *Client) replySnapshotForSend(ctx context.Context, chatID, replyToMessageID string) (appstore.MessageReply, error) {
	replyToMessageID = strings.TrimSpace(replyToMessageID)
	if replyToMessageID == "" {
		return appstore.MessageReply{}, nil
	}

	messageID := replyToMessageID
	if !strings.HasPrefix(messageID, chatID+":") {
		messageID = internalMessageIDForChat(chatID, types.MessageID(messageID))
	}
	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appstore.MessageReply{}, app.NewCommandError(app.CommandErrorNotFound, "reply message not found")
		}
		return appstore.MessageReply{}, err
	}
	if message.ChatID != chatID {
		return appstore.MessageReply{}, app.NewCommandError(app.CommandErrorInvalidArgument, "reply message is not in this chat")
	}

	return replyFromStoredMessage(message), nil
}

func replyFromStoredMessage(message appstore.Message) appstore.MessageReply {
	return appstore.MessageReply{
		MessageID:     message.ID,
		SenderID:      message.SenderID,
		SenderName:    message.SenderName,
		Text:          message.Text,
		MediaKind:     message.MediaKind,
		MediaMimeType: message.MediaMimeType,
		Direction:     message.Direction,
	}
}

// viewOnceSendable reports whether WhatsApp allows sending this media kind
// view-once. Stickers and documents have no view-once form.
func viewOnceSendable(mediaKind string) bool {
	switch mediaKind {
	case appstore.MediaKindImage, appstore.MediaKindVideo, appstore.MediaKindAudio, appstore.MediaKindVoice:
		return true
	default:
		return false
	}
}

// readOutboundMedia validates the file at filePath and classifies it into a
// media kind. An explicit Kind hint forces that kind (with MIME sanity
// checks); otherwise images stay images, video/audio take their kind, and
// anything else (PDFs, zips, text, …) becomes a document. GIFs are still
// rejected: WhatsApp treats them as short videos and the GIF→MP4 transcode
// path does not exist yet.
func readOutboundMedia(filePath string, opts MediaSendOptions) (data []byte, mimeType, extension, mediaKind, fileName string, err error) {
	kindHint := strings.ToLower(strings.TrimSpace(opts.Kind))
	switch kindHint {
	case "", "auto", "image", "video", "audio", "voice", "document":
	default:
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "unknown media kind %q: want image, video, audio, voice or document", opts.Kind)
	}
	if !filepath.IsAbs(filePath) {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file path must be absolute")
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file is not accessible")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be a regular file")
	}
	if info.Size() <= 0 {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file is empty")
	}
	// The size ceiling is per-kind: documents sent via "send as document"
	// are not size- (or duration-) checked like media, up to their own cap.
	sizeCap := int64(maxOutboundMediaBytes)
	if kindHint == "document" {
		sizeCap = maxOutboundDocumentBytes
	}
	if info.Size() > sizeCap {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be <= %d MiB", sizeCap/(1024*1024))
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorRejected, "media file owner is not allowed")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file is not readable")
	}
	defer f.Close()

	data, err = io.ReadAll(io.LimitReader(f, sizeCap+1))
	if err != nil {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file could not be read")
	}
	if len(data) > int(sizeCap) {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be <= %d MiB", sizeCap/(1024*1024))
	}
	mimeType = http.DetectContentType(data)
	// WhatsApp GIFs are short MP4 videos with a gif-playback flag; sending the
	// raw .gif bytes through the image path delivers a static picture. Refuse
	// until a GIF→video transcode path exists.
	if mimeType == "image/gif" {
		return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "GIF files can't be sent yet: WhatsApp treats GIFs as short videos, which whatevr does not support sending")
	}

	sourceBase := filepath.Base(filePath)
	switch {
	case kindHint == "image":
		if !strings.HasPrefix(mimeType, "image/") {
			return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be a supported image")
		}
		mediaKind = appstore.MediaKindImage
		extension, ok = outboundImageExtension(mimeType)
		if !ok {
			return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be a supported image")
		}
		return data, mimeType, extension, mediaKind, "", nil
	case kindHint == "video":
		if !strings.HasPrefix(mimeType, "video/") {
			return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be a video")
		}
		mediaKind = appstore.MediaKindVideo
		return data, mimeType, outboundVideoExtension(sourceBase, mimeType), mediaKind, "", nil
	case kindHint == "audio":
		if !isAudioMime(mimeType, sourceBase) {
			return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be audio")
		}
		mediaKind = appstore.MediaKindAudio
		return data, mimeType, outboundAudioExtension(sourceBase, mimeType), mediaKind, "", nil
	case kindHint == "voice":
		if !isAudioMime(mimeType, sourceBase) {
			return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "voice notes must be audio files")
		}
		mediaKind = appstore.MediaKindVoice
		return data, outboundVoiceMime(mimeType, sourceBase), outboundAudioExtension(sourceBase, mimeType), mediaKind, "", nil
	case kindHint == "document":
		mediaKind = appstore.MediaKindDocument
		fileName = outboundDocumentName(sourceBase, opts.Filename)
		return data, mimeType, outboundDocumentExtension(sourceBase, mimeType), mediaKind, fileName, nil
	}

	// Auto classification.
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		mediaKind = appstore.MediaKindImage
		extension, ok = outboundImageExtension(mimeType)
		if !ok {
			return nil, "", "", "", "", app.NewCommandError(app.CommandErrorInvalidArgument, "media file must be a supported image")
		}
		return data, mimeType, extension, mediaKind, "", nil
	case strings.HasPrefix(mimeType, "video/"):
		mediaKind = appstore.MediaKindVideo
		return data, mimeType, outboundVideoExtension(sourceBase, mimeType), mediaKind, "", nil
	case strings.HasPrefix(mimeType, "audio/"):
		mediaKind = appstore.MediaKindAudio
		return data, mimeType, outboundAudioExtension(sourceBase, mimeType), mediaKind, "", nil
	default:
		mediaKind = appstore.MediaKindDocument
		fileName = outboundDocumentName(sourceBase, opts.Filename)
		return data, mimeType, outboundDocumentExtension(sourceBase, mimeType), mediaKind, fileName, nil
	}
}

func outboundImageExtension(mimeType string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	default:
		return "", false
	}
}

// outboundPreservedExtension keeps the source file's extension when it looks
// sane, so videos, audio and documents land in the cache (and on the
// recipient's phone) with a usable name. Falls back to a MIME-derived default.
func outboundPreservedExtension(sourceBase, mimeType, fallback string) string {
	if ext := strings.ToLower(strings.TrimSpace(filepath.Ext(sourceBase))); ext != "" {
		trimmed := strings.TrimPrefix(ext, ".")
		if len(trimmed) >= 1 && len(trimmed) <= 10 {
			clean := true
			for _, r := range trimmed {
				if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
					continue
				}
				clean = false
				break
			}
			if clean {
				return ext
			}
		}
	}
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return ".mp4"
	case strings.HasPrefix(mimeType, "audio/ogg"):
		return ".ogg"
	case strings.HasPrefix(mimeType, "audio/"):
		return ".m4a"
	case mimeType == "application/pdf":
		return ".pdf"
	default:
		return fallback
	}
}

func outboundVideoExtension(sourceBase, mimeType string) string {
	return outboundPreservedExtension(sourceBase, mimeType, ".mp4")
}

func outboundAudioExtension(sourceBase, mimeType string) string {
	return outboundPreservedExtension(sourceBase, mimeType, ".ogg")
}

// outboundVoiceMime normalizes the sniffed MIME type of a voice note to what
// official clients send: "audio/ogg; codecs=opus". Go's http.DetectContentType
// reports Ogg Opus as "application/ogg", a mimetype no phone client sends for
// PTT — desktop recordings went out with it and never appeared on mobile.
// Only Ogg containers are rewritten; anything else passes through unchanged.
func outboundVoiceMime(mimeType, sourceBase string) string {
	switch {
	case strings.HasPrefix(mimeType, "audio/ogg"),
		mimeType == "audio/opus",
		mimeType == "application/ogg",
		mimeType == "application/x-ogg":
		return "audio/ogg; codecs=opus"
	}
	if mimeType == "application/octet-stream" {
		lower := strings.ToLower(sourceBase)
		if strings.HasSuffix(lower, ".ogg") || strings.HasSuffix(lower, ".oga") || strings.HasSuffix(lower, ".opus") {
			return "audio/ogg; codecs=opus"
		}
	}
	return mimeType
}

// isAudioMime reports whether a sniffed MIME type (plus filename fallback)
// is an audio payload. Go's http.DetectContentType returns
// "application/ogg" for Ogg Opus voice notes, not "audio/ogg", so the Ogg
// container must be accepted explicitly. A .ogg/.oga/.opus filename is also
// accepted when the sniffer returns a generic octet-stream.
func isAudioMime(mimeType, sourceBase string) bool {
	if strings.HasPrefix(mimeType, "audio/") {
		return true
	}
	if mimeType == "application/ogg" || mimeType == "application/x-ogg" {
		return true
	}
	if mimeType == "application/octet-stream" {
		lower := strings.ToLower(sourceBase)
		return strings.HasSuffix(lower, ".ogg") || strings.HasSuffix(lower, ".oga") || strings.HasSuffix(lower, ".opus")
	}
	return false
}

func outboundDocumentExtension(sourceBase, mimeType string) string {
	return outboundPreservedExtension(sourceBase, mimeType, ".bin")
}

// outboundDocumentName picks the display filename for a sent document: an
// explicit override wins, otherwise the source basename, sanitized to one
// path segment of bounded length.
func outboundDocumentName(sourceBase, override string) string {
	if name := strings.TrimSpace(override); name != "" {
		sourceBase = name
	}
	sourceBase = strings.ReplaceAll(sourceBase, "/", "_")
	sourceBase = strings.ReplaceAll(sourceBase, "\\", "_")
	sourceBase = strings.TrimSpace(sourceBase)
	if sourceBase == "" || sourceBase == "." || sourceBase == ".." {
		sourceBase = "file"
	}
	if runes := []rune(sourceBase); len(runes) > 128 {
		sourceBase = string(runes[:128])
	}
	return sourceBase
}

const outgoingThumbnailMaxDimension = 100

// standardImageMaxSide is the longest side WhatsApp-scale photos are
// downscaled to in standard quality, matching official clients. HD sends the
// original bytes untouched.
const standardImageMaxSide = 1600

// downscaleImageForStandard downsizes data when its longest side exceeds
// standardImageMaxSide, re-encoding as JPEG (like official clients) except
// for PNG sources, which stay PNG to preserve transparency. Images already
// within bounds return untouched. It returns the bytes, MIME type, extension
// and dimensions to store.
func downscaleImageForStandard(data []byte, mimeType string) ([]byte, string, string, int32, int32) {
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mimeType, "", 0, 0
	}
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return data, mimeType, "", 0, 0
	}
	if srcW <= standardImageMaxSide && srcH <= standardImageMaxSide {
		extension, ok := outboundImageExtension(mimeType)
		if !ok {
			extension = ".jpg"
		}
		return data, mimeType, extension, int32(srcW), int32(srcH)
	}
	dstW, dstH := thumbnailDimensions(srcW, srcH, standardImageMaxSide)
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for dy := 0; dy < dstH; dy++ {
		y0 := bounds.Min.Y + dy*srcH/dstH
		y1 := bounds.Min.Y + (dy+1)*srcH/dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := bounds.Min.X + dx*srcW/dstW
			x1 := bounds.Min.X + (dx+1)*srcW/dstW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rSum, gSum, bSum, aSum uint64
			var count uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, b, a := src.At(x, y).RGBA()
					rSum += uint64(r)
					gSum += uint64(g)
					bSum += uint64(b)
					aSum += uint64(a)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			dst.SetRGBA(dx, dy, color.RGBA{
				R: uint8((rSum / count) >> 8),
				G: uint8((gSum / count) >> 8),
				B: uint8((bSum / count) >> 8),
				A: uint8((aSum / count) >> 8),
			})
		}
	}
	var buf bytes.Buffer
	outMime := mimeType
	extension := ".jpg"
	if format == "png" || mimeType == "image/png" {
		if err := png.Encode(&buf, dst); err != nil {
			return data, mimeType, "", 0, 0
		}
		outMime = "image/png"
		extension = ".png"
	} else {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
			return data, mimeType, "", 0, 0
		}
		outMime = "image/jpeg"
		extension = ".jpg"
	}
	return buf.Bytes(), outMime, extension, int32(dstW), int32(dstH)
}

// outgoingImageThumbnail produces a small inline JPEG thumbnail for an outgoing
// image, matching official-client behaviour. It returns nil when the image
// can't be decoded. A simple box-average downscale is used to avoid pulling in
// a resize dependency; thumbnails are tiny so quality is sufficient.
func outgoingImageThumbnail(data []byte) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil
	}

	dstW, dstH := thumbnailDimensions(srcW, srcH, outgoingThumbnailMaxDimension)
	thumb := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for dy := 0; dy < dstH; dy++ {
		y0 := bounds.Min.Y + dy*srcH/dstH
		y1 := bounds.Min.Y + (dy+1)*srcH/dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := bounds.Min.X + dx*srcW/dstW
			x1 := bounds.Min.X + (dx+1)*srcW/dstW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rSum, gSum, bSum, count uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, b, _ := src.At(x, y).RGBA()
					rSum += uint64(r)
					gSum += uint64(g)
					bSum += uint64(b)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			thumb.SetRGBA(dx, dy, color.RGBA{
				R: uint8((rSum / count) >> 8),
				G: uint8((gSum / count) >> 8),
				B: uint8((bSum / count) >> 8),
				A: 0xFF,
			})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 70}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// thumbnailDimensions scales (srcW, srcH) so the longest side is at most max,
// preserving aspect ratio. Images already within bounds are returned unchanged.
func thumbnailDimensions(srcW, srcH, max int) (int, int) {
	if srcW <= max && srcH <= max {
		return srcW, srcH
	}
	if srcW >= srcH {
		h := srcH * max / srcW
		if h < 1 {
			h = 1
		}
		return max, h
	}
	w := srcW * max / srcH
	if w < 1 {
		w = 1
	}
	return w, max
}

func safeFilenamePart(input string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "@", "_")
	return replacer.Replace(input)
}

func (c *Client) signalSendQueue() {
	select {
	case c.sendQueueWake <- struct{}{}:
	default:
	}
}

func (c *Client) runSendQueue(ctx context.Context) {
	c.signalSendQueue()

	retry := time.NewTimer(0)
	if !retry.Stop() {
		<-retry.C
	}
	defer retry.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.sendQueueWake:
		}

		for {
			c.sendQueueMu.Lock()
			processed, err := c.drainSendQueue(ctx)
			c.sendQueueMu.Unlock()
			if err != nil {
				retry.Reset(5 * time.Second)
				select {
				case <-ctx.Done():
					return
				case <-c.sendQueueWake:
					if !retry.Stop() {
						<-retry.C
					}
				case <-retry.C:
				}
				continue
			}
			if !processed {
				break
			}
		}
	}
}

// sendQueueBackoff returns how long to wait before retrying a message
// that has failed attempt times.
func sendQueueBackoff(attempt int32) time.Duration {
	const base = 10 * time.Second
	const max = 5 * time.Minute
	if attempt <= 0 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 5 {
		shift = 5
	}
	delay := base * (1 << uint(shift))
	if delay > max {
		delay = max
	}
	return delay
}

func (c *Client) drainSendQueue(ctx context.Context) (bool, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return false, fmt.Errorf("WhatsApp client is not online")
	}

	pending, err := c.store.ListPendingOutgoingMessages(ctx, 25, time.Now())
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, nil
	}

	for _, message := range pending {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		if err := c.sendPendingMessage(ctx, client, message); err != nil {
			// Drop the timing entry; the retry attempt re-times from pickup.
			c.finishSendTiming(message.ID)
			return true, err
		}
	}

	return true, nil
}

func (c *Client) sendPendingMessage(ctx context.Context, client *whatsmeow.Client, message appstore.Message) error {
	c.markSendTiming(message.ID, func(t *sendTiming) { t.queuePickup = time.Now() })
	targetJID, err := types.ParseJID(message.ChatID)
	if err != nil {
		c.markPendingMessageFailed(ctx, message.ID, "invalid chat ID")
		return nil
	}

	externalID := types.MessageID(appstore.ExternalMessageID(message.ChatID, message.ID))
	if message.MediaMimeType != "" || message.MediaLocalPath != "" {
		return c.sendPendingMediaMessage(ctx, client, targetJID, externalID, message)
	}

	outgoingMessage := &waE2E.Message{Conversation: proto.String(message.Text)}
	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		outgoingMessage = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(message.Text),
				ContextInfo: contextInfo,
			},
		}
	}

	if _, err := client.SendMessage(ctx, targetJID, outgoingMessage, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		// Transient: back off and retry.
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send message %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return fmt.Errorf("send text %s: %w", message.ID, err)
	}

	c.markPendingMessageSent(ctx, message.ID)
	return nil
}

func (c *Client) sendPendingMediaMessage(ctx context.Context, client *whatsmeow.Client, targetJID types.JID, externalID types.MessageID, message appstore.Message) error {
	if message.MediaKind == appstore.MediaKindSticker {
		return c.sendPendingStickerMessage(ctx, client, targetJID, externalID, message)
	}

	data, err := os.ReadFile(message.MediaLocalPath)
	if err != nil {
		// Forwarded copies of an undownloaded message carry only the original
		// sender's media payload; resend those keys instead of re-uploading.
		if sent, payloadErr := c.sendPendingMediaFromPayload(ctx, client, targetJID, externalID, message); sent || payloadErr != nil {
			return payloadErr
		}
		c.markPendingMessageFailed(ctx, message.ID, "cached media file missing or unreadable")
		return nil
	}

	mimeType := message.MediaMimeType
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	// View-once forwards are forbidden by WhatsApp; a queued view-once row is
	// always our own fresh send (ForwardMessage rejects view-once sources).
	outgoing, payloadProto, uploadType, buildErr := buildOutgoingMediaMessage(message, data, mimeType)
	if buildErr != nil {
		c.markPendingMessageFailed(ctx, message.ID, buildErr.Error())
		return nil
	}

	resp, err := client.Upload(ctx, data, uploadType)
	if err != nil {
		return fmt.Errorf("upload media %s: %w", message.ID, err)
	}
	fillOutgoingMediaUpload(payloadProto, resp)
	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		setOutgoingMediaContext(payloadProto, contextInfo)
	}
	if message.IsViewOnce {
		outgoing = &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: outgoing}}
	}

	if _, err := client.SendMessage(ctx, targetJID, outgoing, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send media %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return fmt.Errorf("send media %s: %w", message.ID, err)
	}

	// Persist the sent proto (including thumbnail/media keys) so a later reply
	// quoting our own media can be reconstructed losslessly. The inner proto
	// is stored, never the view-once wrapper, so forwards reuse the keys.
	if payload, marshalErr := proto.Marshal(payloadProto); marshalErr == nil {
		if _, dbErr := c.store.UpdateMessageMediaPayload(ctx, message.ID, payload); dbErr != nil {
			c.log.Warnf("Failed to persist sent media payload for %s: %v", message.ID, dbErr)
		}
	}

	c.markPendingMessageSent(ctx, message.ID)
	return nil
}

// buildOutgoingMediaMessage assembles the wire message for a queued media row.
// It returns the envelope, the inner media proto (which carries the upload
// fields and is persisted for quotes/forwards), and the upload media type.
// The caller fills upload fields, attaches context info, and optionally wraps
// the envelope view-once.
func buildOutgoingMediaMessage(message appstore.Message, data []byte, mimeType string) (envelope *waE2E.Message, inner proto.Message, uploadType whatsmeow.MediaType, err error) {
	switch message.MediaKind {
	case "", appstore.MediaKindImage:
		imgMsg := &waE2E.ImageMessage{
			Caption:  proto.String(message.Text),
			Mimetype: proto.String(mimeType),
		}
		// Embed a small inline thumbnail like official clients do, so
		// recipients have a preview while the full image downloads and so
		// quotes of this image (here or on the other side) render one.
		if thumb := outgoingImageThumbnail(data); len(thumb) > 0 {
			imgMsg.JPEGThumbnail = thumb
		}
		if message.MediaWidth > 0 {
			imgMsg.Width = proto.Uint32(uint32(message.MediaWidth))
		}
		if message.MediaHeight > 0 {
			imgMsg.Height = proto.Uint32(uint32(message.MediaHeight))
		}
		return &waE2E.Message{ImageMessage: imgMsg}, imgMsg, whatsmeow.MediaImage, nil
	case appstore.MediaKindVideo, appstore.MediaKindGIF:
		videoMsg := &waE2E.VideoMessage{
			Caption:  proto.String(message.Text),
			Mimetype: proto.String(mimeType),
		}
		if message.MediaDurationSecs > 0 {
			videoMsg.Seconds = proto.Uint32(uint32(message.MediaDurationSecs))
		}
		if message.MediaWidth > 0 {
			videoMsg.Width = proto.Uint32(uint32(message.MediaWidth))
		}
		if message.MediaHeight > 0 {
			videoMsg.Height = proto.Uint32(uint32(message.MediaHeight))
		}
		if message.MediaKind == appstore.MediaKindGIF {
			videoMsg.GifPlayback = proto.Bool(true)
		}
		return &waE2E.Message{VideoMessage: videoMsg}, videoMsg, whatsmeow.MediaVideo, nil
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		audioMsg := &waE2E.AudioMessage{
			Mimetype: proto.String(mimeType),
		}
		if message.MediaKind == appstore.MediaKindVoice {
			audioMsg.PTT = proto.Bool(true)
		}
		if message.MediaDurationSecs > 0 {
			audioMsg.Seconds = proto.Uint32(uint32(message.MediaDurationSecs))
		}
		if len(message.MediaWaveform) > 0 {
			audioMsg.Waveform = message.MediaWaveform
		}
		return &waE2E.Message{AudioMessage: audioMsg}, audioMsg, whatsmeow.MediaAudio, nil
	case appstore.MediaKindDocument:
		fileName := strings.TrimSpace(message.MediaFileName)
		if fileName == "" {
			fileName = "file"
		}
		docMsg := &waE2E.DocumentMessage{
			FileName: proto.String(fileName),
			Mimetype: proto.String(mimeType),
		}
		if strings.TrimSpace(message.Text) != "" {
			docMsg.Caption = proto.String(message.Text)
		}
		if message.MediaPageCount > 0 {
			docMsg.PageCount = proto.Uint32(uint32(message.MediaPageCount))
		}
		return &waE2E.Message{DocumentMessage: docMsg}, docMsg, whatsmeow.MediaDocument, nil
	default:
		return nil, nil, "", errors.New("unsupported media kind for sending")
	}
}

// fillOutgoingMediaUpload copies an upload response into the inner media
// proto of an envelope built by buildOutgoingMediaMessage.
func fillOutgoingMediaUpload(inner proto.Message, resp whatsmeow.UploadResponse) {
	switch msg := inner.(type) {
	case *waE2E.ImageMessage:
		msg.URL = &resp.URL
		msg.DirectPath = &resp.DirectPath
		msg.MediaKey = resp.MediaKey
		msg.FileEncSHA256 = resp.FileEncSHA256
		msg.FileSHA256 = resp.FileSHA256
		msg.FileLength = &resp.FileLength
	case *waE2E.VideoMessage:
		msg.URL = &resp.URL
		msg.DirectPath = &resp.DirectPath
		msg.MediaKey = resp.MediaKey
		msg.FileEncSHA256 = resp.FileEncSHA256
		msg.FileSHA256 = resp.FileSHA256
		msg.FileLength = &resp.FileLength
	case *waE2E.AudioMessage:
		msg.URL = &resp.URL
		msg.DirectPath = &resp.DirectPath
		msg.MediaKey = resp.MediaKey
		msg.FileEncSHA256 = resp.FileEncSHA256
		msg.FileSHA256 = resp.FileSHA256
		msg.FileLength = &resp.FileLength
	case *waE2E.DocumentMessage:
		msg.URL = &resp.URL
		msg.DirectPath = &resp.DirectPath
		msg.MediaKey = resp.MediaKey
		msg.FileEncSHA256 = resp.FileEncSHA256
		msg.FileSHA256 = resp.FileSHA256
		msg.FileLength = &resp.FileLength
	}
}

// setOutgoingMediaContext attaches reply/forward/mention context to the inner
// media proto of an envelope built by buildOutgoingMediaMessage.
func setOutgoingMediaContext(inner proto.Message, contextInfo *waE2E.ContextInfo) {
	switch msg := inner.(type) {
	case *waE2E.ImageMessage:
		msg.ContextInfo = contextInfo
	case *waE2E.VideoMessage:
		msg.ContextInfo = contextInfo
	case *waE2E.AudioMessage:
		msg.ContextInfo = contextInfo
	case *waE2E.DocumentMessage:
		msg.ContextInfo = contextInfo
	}
}

// sendPendingStickerMessage sends a queued sticker. The first send of a
// sticker uploads its cached file once and persists the resulting media keys
// (stickers.upload_payload); every later send of the same sticker skips the
// upload entirely. A send failure on reused keys invalidates the cache and
// retries with a fresh upload via the normal backoff.
func (c *Client) sendPendingStickerMessage(ctx context.Context, client *whatsmeow.Client, targetJID types.JID, externalID types.MessageID, message appstore.Message) error {
	sticker, ok, err := c.store.GetSticker(ctx, message.MediaCacheKey)
	if err != nil {
		return fmt.Errorf("load sticker %s: %w", message.MediaCacheKey, err)
	}
	if !ok {
		// Forwarded stickers usually aren't in the library; resend the
		// original media keys carried in the copied payload.
		if sent, payloadErr := c.sendPendingMediaFromPayload(ctx, client, targetJID, externalID, message); sent || payloadErr != nil {
			return payloadErr
		}
		c.markPendingMessageFailed(ctx, message.ID, "sticker is no longer in the library")
		return nil
	}

	var stickerMsg *waE2E.StickerMessage
	reusedUpload := false
	if len(sticker.UploadPayload) > 0 && time.Since(time.Unix(sticker.UploadTS, 0)) < stickerUploadReuseTTL {
		var cached waE2E.StickerMessage
		if proto.Unmarshal(sticker.UploadPayload, &cached) == nil && cached.GetDirectPath() != "" {
			stickerMsg = &cached
			reusedUpload = true
		}
	}
	if stickerMsg == nil {
		stickerMsg, err = c.uploadStickerForSend(ctx, client, sticker)
		if err != nil {
			newAttempts := message.SendAttempts + 1
			delay := sendQueueBackoff(newAttempts)
			c.log.Warnf("Failed to upload sticker %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
			if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
				c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
			}
			return fmt.Errorf("upload sticker %s: %w", message.ID, err)
		}
		if payload, marshalErr := proto.Marshal(stickerMsg); marshalErr == nil {
			if dbErr := c.store.SetStickerUploadPayload(ctx, sticker.CacheKey, payload, time.Now()); dbErr != nil {
				c.log.Warnf("Failed to cache sticker upload for %s: %v", sticker.CacheKey, dbErr)
			}
		}
	}

	outgoing := proto.Clone(stickerMsg).(*waE2E.StickerMessage)
	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		outgoing.ContextInfo = contextInfo
	}

	if _, err := client.SendMessage(ctx, targetJID, &waE2E.Message{StickerMessage: outgoing}, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		if reusedUpload {
			// The cached upload may have expired server-side; force a fresh
			// upload on the next attempt.
			if dbErr := c.store.SetStickerUploadPayload(ctx, sticker.CacheKey, nil, time.Now()); dbErr != nil {
				c.log.Warnf("Failed to invalidate sticker upload for %s: %v", sticker.CacheKey, dbErr)
			}
		}
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send sticker %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return fmt.Errorf("send sticker %s: %w", message.ID, err)
	}

	// Persist the sent proto (our upload's media keys, no context info) so a
	// later reply quoting this sticker reconstructs losslessly.
	if payload, marshalErr := proto.Marshal(stickerMsg); marshalErr == nil {
		if _, dbErr := c.store.UpdateMessageMediaPayload(ctx, message.ID, payload); dbErr != nil {
			c.log.Warnf("Failed to persist sent sticker payload for %s: %v", message.ID, dbErr)
		}
	}

	c.markPendingMessageSent(ctx, message.ID)
	return nil
}

// outgoingContextInfo combines the reply quote (if any) with WhatsApp's
// forwarded marker for messages queued by ForwardMessage.
func (c *Client) outgoingContextInfo(ctx context.Context, client *whatsmeow.Client, message appstore.Message) *waE2E.ContextInfo {
	contextInfo := c.outgoingReplyContextInfo(ctx, client, message)
	if message.IsForwarded {
		if contextInfo == nil {
			contextInfo = &waE2E.ContextInfo{}
		}
		contextInfo.IsForwarded = proto.Bool(true)
		contextInfo.ForwardingScore = proto.Uint32(1)
	}
	if mentionedJIDs := appstore.MentionJIDs(message.Mentions); len(mentionedJIDs) > 0 {
		if contextInfo == nil {
			contextInfo = &waE2E.ContextInfo{}
		}
		contextInfo.MentionedJID = mentionedJIDs
	}
	return contextInfo
}

// sendPendingMediaFromPayload sends a media message straight from its stored
// WhatsApp proto (original media keys, no upload). Returns sent=false when the
// payload is missing or unusable so the caller can fall through.
func (c *Client) sendPendingMediaFromPayload(ctx context.Context, client *whatsmeow.Client, targetJID types.JID, externalID types.MessageID, message appstore.Message) (bool, error) {
	if len(message.MediaPayload) == 0 {
		return false, nil
	}

	var outgoing *waE2E.Message
	switch message.MediaKind {
	case appstore.MediaKindSticker:
		sticker := &waE2E.StickerMessage{}
		if proto.Unmarshal(message.MediaPayload, sticker) != nil || sticker.GetDirectPath() == "" {
			return false, nil
		}
		clone := proto.Clone(sticker).(*waE2E.StickerMessage)
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		outgoing = &waE2E.Message{StickerMessage: clone}
	case appstore.MediaKindVideo, appstore.MediaKindGIF:
		video := &waE2E.VideoMessage{}
		if proto.Unmarshal(message.MediaPayload, video) != nil || video.GetDirectPath() == "" {
			return false, nil
		}
		clone := proto.Clone(video).(*waE2E.VideoMessage)
		if message.Text != "" {
			clone.Caption = proto.String(message.Text)
		}
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		outgoing = &waE2E.Message{VideoMessage: clone}
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		audio := &waE2E.AudioMessage{}
		if proto.Unmarshal(message.MediaPayload, audio) != nil || audio.GetDirectPath() == "" {
			return false, nil
		}
		clone := proto.Clone(audio).(*waE2E.AudioMessage)
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		outgoing = &waE2E.Message{AudioMessage: clone}
	case appstore.MediaKindDocument:
		document := &waE2E.DocumentMessage{}
		if proto.Unmarshal(message.MediaPayload, document) != nil || document.GetDirectPath() == "" {
			return false, nil
		}
		clone := proto.Clone(document).(*waE2E.DocumentMessage)
		if message.Text != "" {
			clone.Caption = proto.String(message.Text)
		}
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		outgoing = &waE2E.Message{DocumentMessage: clone}
	default:
		img := &waE2E.ImageMessage{}
		if proto.Unmarshal(message.MediaPayload, img) != nil || img.GetDirectPath() == "" {
			return false, nil
		}
		clone := proto.Clone(img).(*waE2E.ImageMessage)
		if message.Text != "" {
			clone.Caption = proto.String(message.Text)
		}
		clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
		outgoing = &waE2E.Message{ImageMessage: clone}
	}

	if _, err := client.SendMessage(ctx, targetJID, outgoing, whatsmeow.SendRequestExtra{ID: externalID}); err != nil {
		newAttempts := message.SendAttempts + 1
		delay := sendQueueBackoff(newAttempts)
		c.log.Warnf("Failed to send media payload %s (attempt %d), retry in %s: %v", message.ID, newAttempts, delay, err)
		if dbErr := c.store.UpdateMessageSendAttempt(ctx, message.ID, newAttempts, err.Error(), time.Now().Add(delay)); dbErr != nil {
			c.log.Warnf("Failed to record send attempt for %s: %v", message.ID, dbErr)
		}
		return true, fmt.Errorf("send media payload %s: %w", message.ID, err)
	}

	c.markPendingMessageSent(ctx, message.ID)
	return true, nil
}

func (c *Client) outgoingReplyContextInfo(ctx context.Context, client *whatsmeow.Client, message appstore.Message) *waE2E.ContextInfo {
	if message.ReplyTo.MessageID == "" {
		return nil
	}

	// Prefer the original stored message so the quote carries the full media
	// proto (thumbnail, media keys, URL) recipients render the preview from.
	// Fall back to the thin reply summary only if the original is gone.
	stanzaSource := message.ReplyTo.MessageID
	senderID := message.ReplyTo.SenderID
	var quoted *waE2E.Message
	if original, err := c.store.GetMessage(ctx, message.ReplyTo.MessageID); err == nil {
		quoted = quotedMessageFromStored(original)
		stanzaSource = original.ID
		senderID = original.SenderID
	} else {
		quoted = quotedMessageForReply(message.ReplyTo)
	}

	contextInfo := &waE2E.ContextInfo{
		// RemoteJID is intentionally omitted for same-chat replies; setting it
		// can break jump-to on official clients.
		StanzaID:      proto.String(appstore.ExternalMessageID(message.ChatID, stanzaSource)),
		QuotedMessage: quoted,
	}
	if participant := c.outgoingReplyParticipant(ctx, client, message.ChatID, senderID); participant != "" {
		contextInfo.Participant = proto.String(participant)
	}
	return contextInfo
}

func (c *Client) outgoingReplyParticipant(ctx context.Context, client *whatsmeow.Client, chatID, senderID string) string {
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return ""
	}
	if senderID == "me" {
		if client == nil || client.Store.ID == nil {
			return ""
		}
		return client.Store.ID.ToNonAD().String()
	}
	jid, err := types.ParseJID(senderID)
	if err != nil {
		return senderID
	}
	// DMs are sent PN-addressed, so a LID participant won't match on the
	// recipient (breaks name resolution and jump-to). Normalize to the chat's
	// addressing mode; groups keep the stored member JID.
	if chatJID, err := types.ParseJID(chatID); err == nil && chatJID.Server != types.GroupServer && chatJID.Server != types.BroadcastServer {
		jid = c.normalizeJIDForChat(ctx, jid)
	}
	return jid.String()
}

// buildEditContent assembles the replacement message body for an edit. For a
// text message it mirrors the normal send path (Conversation, or
// ExtendedTextMessage when a reply quote/forward context must be preserved).
// For an image or video message only the caption is editable, so it reuses
// the persisted media proto (keys/URL/thumbnail) and swaps the caption rather
// than re-uploading. It returns nil for content that cannot be edited (e.g. a
// sticker, which has no caption, or media whose original proto is missing).
func (c *Client) buildEditContent(ctx context.Context, client *whatsmeow.Client, message appstore.Message, newText string) *waE2E.Message {
	if message.MediaMimeType != "" || message.MediaLocalPath != "" {
		switch message.MediaKind {
		case appstore.MediaKindImage:
			img := &waE2E.ImageMessage{}
			if len(message.MediaPayload) == 0 || proto.Unmarshal(message.MediaPayload, img) != nil || img.GetDirectPath() == "" {
				return nil
			}
			clone := proto.Clone(img).(*waE2E.ImageMessage)
			clone.Caption = proto.String(newText)
			clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
			return &waE2E.Message{ImageMessage: clone}
		case appstore.MediaKindVideo:
			video := &waE2E.VideoMessage{}
			if len(message.MediaPayload) == 0 || proto.Unmarshal(message.MediaPayload, video) != nil || video.GetDirectPath() == "" {
				return nil
			}
			clone := proto.Clone(video).(*waE2E.VideoMessage)
			clone.Caption = proto.String(newText)
			clone.ContextInfo = c.outgoingContextInfo(ctx, client, message)
			return &waE2E.Message{VideoMessage: clone}
		default:
			return nil
		}
	}

	if contextInfo := c.outgoingContextInfo(ctx, client, message); contextInfo != nil {
		return &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(newText),
				ContextInfo: contextInfo,
			},
		}
	}
	return &waE2E.Message{Conversation: proto.String(newText)}
}

// quotedMessageFromStored rebuilds the WhatsApp message proto to embed as the
// quoted message in a reply, reusing the full media sub-proto persisted in
// media_payload so stickers and photos render their thumbnails on the
// recipient's side.
func quotedMessageFromStored(message appstore.Message) *waE2E.Message {
	switch message.MediaKind {
	case appstore.MediaKindSticker:
		sticker := &waE2E.StickerMessage{}
		if len(message.MediaPayload) > 0 {
			if err := proto.Unmarshal(message.MediaPayload, sticker); err != nil {
				sticker = &waE2E.StickerMessage{}
			}
		}
		if sticker.Mimetype == nil && message.MediaMimeType != "" {
			sticker.Mimetype = proto.String(message.MediaMimeType)
		}
		return &waE2E.Message{StickerMessage: sticker}
	case appstore.MediaKindImage:
		img := &waE2E.ImageMessage{}
		if len(message.MediaPayload) > 0 {
			if err := proto.Unmarshal(message.MediaPayload, img); err != nil {
				img = &waE2E.ImageMessage{}
			}
		}
		if img.Mimetype == nil && message.MediaMimeType != "" {
			img.Mimetype = proto.String(message.MediaMimeType)
		}
		if img.Caption == nil && message.Text != "" {
			img.Caption = proto.String(message.Text)
		}
		return &waE2E.Message{ImageMessage: img}
	case appstore.MediaKindVideo, appstore.MediaKindGIF:
		video := &waE2E.VideoMessage{}
		if len(message.MediaPayload) > 0 {
			if err := proto.Unmarshal(message.MediaPayload, video); err != nil {
				video = &waE2E.VideoMessage{}
			}
		}
		if video.Mimetype == nil && message.MediaMimeType != "" {
			video.Mimetype = proto.String(message.MediaMimeType)
		}
		if video.Caption == nil && message.Text != "" {
			video.Caption = proto.String(message.Text)
		}
		if message.MediaKind == appstore.MediaKindGIF && video.GifPlayback == nil {
			video.GifPlayback = proto.Bool(true)
		}
		return &waE2E.Message{VideoMessage: video}
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		audio := &waE2E.AudioMessage{}
		if len(message.MediaPayload) > 0 {
			if err := proto.Unmarshal(message.MediaPayload, audio); err != nil {
				audio = &waE2E.AudioMessage{}
			}
		}
		if audio.Mimetype == nil && message.MediaMimeType != "" {
			audio.Mimetype = proto.String(message.MediaMimeType)
		}
		if message.MediaKind == appstore.MediaKindVoice && audio.PTT == nil {
			audio.PTT = proto.Bool(true)
		}
		return &waE2E.Message{AudioMessage: audio}
	case appstore.MediaKindDocument:
		document := &waE2E.DocumentMessage{}
		if len(message.MediaPayload) > 0 {
			if err := proto.Unmarshal(message.MediaPayload, document); err != nil {
				document = &waE2E.DocumentMessage{}
			}
		}
		if document.Mimetype == nil && message.MediaMimeType != "" {
			document.Mimetype = proto.String(message.MediaMimeType)
		}
		if document.Caption == nil && message.Text != "" {
			document.Caption = proto.String(message.Text)
		}
		return &waE2E.Message{DocumentMessage: document}
	default:
		if message.Text != "" {
			return &waE2E.Message{Conversation: proto.String(message.Text)}
		}
		return &waE2E.Message{Conversation: proto.String(replyMediaSummary(appstore.MessageReply{
			Text:          message.Text,
			MediaKind:     message.MediaKind,
			MediaMimeType: message.MediaMimeType,
		}))}
	}
}

func quotedMessageForReply(reply appstore.MessageReply) *waE2E.Message {
	if reply.MediaKind == appstore.MediaKindSticker {
		sticker := &waE2E.StickerMessage{}
		if reply.MediaMimeType != "" {
			sticker.Mimetype = proto.String(reply.MediaMimeType)
		}
		return &waE2E.Message{StickerMessage: sticker}
	}
	if reply.MediaKind == appstore.MediaKindImage || strings.HasPrefix(reply.MediaMimeType, "image/") {
		image := &waE2E.ImageMessage{}
		if reply.Text != "" {
			image.Caption = proto.String(reply.Text)
		}
		if reply.MediaMimeType != "" {
			image.Mimetype = proto.String(reply.MediaMimeType)
		}
		return &waE2E.Message{ImageMessage: image}
	}
	// The quoted stub only has to be the right shape for the recipient's
	// renderer; the bytes themselves are never fetched from a quote.
	switch reply.MediaKind {
	case appstore.MediaKindVideo, appstore.MediaKindGIF, appstore.MediaKindVideoNote:
		video := &waE2E.VideoMessage{}
		if reply.Text != "" {
			video.Caption = proto.String(reply.Text)
		}
		if reply.MediaMimeType != "" {
			video.Mimetype = proto.String(reply.MediaMimeType)
		}
		if reply.MediaKind == appstore.MediaKindGIF {
			video.GifPlayback = proto.Bool(true)
		}
		if reply.MediaKind == appstore.MediaKindVideoNote {
			return &waE2E.Message{PtvMessage: video}
		}
		return &waE2E.Message{VideoMessage: video}
	case appstore.MediaKindVoice, appstore.MediaKindAudio:
		audio := &waE2E.AudioMessage{}
		if reply.MediaMimeType != "" {
			audio.Mimetype = proto.String(reply.MediaMimeType)
		}
		if reply.MediaKind == appstore.MediaKindVoice {
			audio.PTT = proto.Bool(true)
		}
		return &waE2E.Message{AudioMessage: audio}
	case appstore.MediaKindDocument:
		document := &waE2E.DocumentMessage{}
		if reply.Text != "" {
			document.FileName = proto.String(reply.Text)
		}
		if reply.MediaMimeType != "" {
			document.Mimetype = proto.String(reply.MediaMimeType)
		}
		return &waE2E.Message{DocumentMessage: document}
	}
	if reply.Text != "" {
		return &waE2E.Message{Conversation: proto.String(reply.Text)}
	}
	return &waE2E.Message{Conversation: proto.String(replyMediaSummary(reply))}
}

// replyMediaSummary is the stand-in body we put in a quote we could not rebuild
// from the original media. It reads the same as everything else the daemon
// renders on one line, and the peer's client shows it verbatim inside the
// quote strip.
func replyMediaSummary(reply appstore.MessageReply) string {
	if line := appstore.ReplyPreviewLine(reply); line != "" {
		return line
	}
	return "Message"
}

func (c *Client) markPendingMessageSent(ctx context.Context, messageID string) {
	c.markSendTiming(messageID, func(t *sendTiming) { t.ackReturn = time.Now() })
	message, changed, err := c.store.UpdateMessageStatus(ctx, messageID, appstore.StatusSent)
	if err != nil {
		c.finishSendTiming(messageID)
		c.log.Warnf("Failed to mark queued message %s sent: %v", messageID, err)
		return
	}
	c.markSendTiming(messageID, func(t *sendTiming) { t.statusWrite = time.Now() })
	if changed {
		c.publishMessageStatusUpdated(ctx, message)
	}
	c.logSendTimeline(messageID, c.finishSendTiming(messageID))
}

func (c *Client) markPendingMessageFailed(ctx context.Context, messageID string, reason string) {
	c.finishSendTiming(messageID)
	message, changed, err := c.store.UpdateMessageStatus(ctx, messageID, appstore.StatusFailed)
	if err != nil {
		c.log.Warnf("Failed to mark queued message %s failed: %v", messageID, err)
		return
	}
	c.log.Warnf("Queued message %s permanently failed: %s", messageID, reason)
	if changed {
		c.publishMessageStatusUpdated(ctx, message)
	}
}

func (c *Client) handleReceipt(evt *events.Receipt, offlineSync bool) {
	status, ok := receiptStatus(evt.Type)
	if !ok {
		return
	}

	ctx := c.backgroundContext()
	normalizedChat := c.normalizeJIDForChat(ctx, evt.Chat)
	chatID := normalizedChat.String()
	if chatID == "" {
		return
	}

	// Viewed receipts on our statuses record who saw them. The status message
	// is not a chat row, so nothing below applies; viewers land in the
	// status_viewers table for the `status.viewers` query.
	if normalizedChat == types.StatusBroadcastJID {
		c.recordStatusViewers(ctx, evt)
		return
	}

	isGroup := normalizedChat.Server == types.GroupServer || normalizedChat.Server == types.BroadcastServer
	kind, isParticipantReceipt := participantReceiptKind(evt.Type)
	clearUnread := receiptClearsLocalUnread(evt)

	participant := ""
	if isParticipantReceipt && !clearUnread {
		own := c.ownParticipantJIDs()
		canonical := c.canonicalParticipantJID(ctx, evt.Sender)
		isSelf := canonical == "" || own[canonical]
		switch {
		case !isGroup && isSelf:
			// 1:1 receipts may arrive without a usable sender; they can only
			// come from the peer.
			participant = c.canonicalParticipantJID(ctx, normalizedChat)
		case isSelf:
			// A non-read group receipt from our own device says nothing about other
			// members; treat it as a plain status update.
			isParticipantReceipt = false
		default:
			participant = canonical
		}
		if participant == "" {
			isParticipantReceipt = false
		}
	}

	if !clearUnread && isParticipantReceipt {
		var participants []string
		participantsKnown := false
		if isGroup {
			participants, participantsKnown = c.groupReceiptParticipants(ctx, normalizedChat)
		}
		for _, messageID := range evt.MessageIDs {
			internalID := internalMessageIDForChat(chatID, messageID)
			message, changed, err := c.applyParticipantReceipt(ctx, internalID, participant, kind, evt.Timestamp, status, participants, participantsKnown, offlineSync)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				c.log.Errorf("Failed to update message status for %s: %v", internalID, err)
				continue
			}
			if changed && !offlineSync {
				c.publishMessageStatusUpdated(ctx, message)
			}
		}
	} else if !clearUnread {
		// One transaction for the whole receipt. This runs on whatsmeow's
		// serialized queue holding the single write connection, and a receipt
		// naming two hundred messages used to mean two hundred commits.
		internalIDs := make([]string, 0, len(evt.MessageIDs))
		for _, messageID := range evt.MessageIDs {
			internalIDs = append(internalIDs, internalMessageIDForChat(chatID, messageID))
		}
		messages, err := c.store.UpdateMessagesStatus(ctx, internalIDs, status)
		if err != nil {
			c.log.Errorf("Failed to update message status in %s: %v", chatID, err)
			return
		}
		if !offlineSync {
			for _, message := range messages {
				c.publishMessageStatusUpdated(ctx, message)
			}
		}
	}

	// A self receipt means we read these messages on another device (the
	// phone): clear them from the chat's unread badge. Published even during
	// offline sync — one ChatUpdated per receipt keeps the badge honest.
	if clearUnread {
		internalIDs := make([]string, 0, len(evt.MessageIDs))
		for _, messageID := range evt.MessageIDs {
			internalIDs = append(internalIDs, internalMessageIDForChat(chatID, messageID))
		}
		chat, changed, err := c.store.MarkMessagesReadByIDs(ctx, chatID, internalIDs)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				c.log.Warnf("Failed to mark self-read messages in %s: %v", chatID, err)
			}
			return
		}
		if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
}

func receiptClearsLocalUnread(evt *events.Receipt) bool {
	switch evt.Type {
	case types.ReceiptTypeReadSelf, types.ReceiptTypePlayedSelf:
		return true
	case types.ReceiptTypeRead, types.ReceiptTypePlayed:
		return evt.IsFromMe
	default:
		return false
	}
}

// applyParticipantReceipt records one participant's receipt and derives the
// message's aggregate status. 1:1 chats keep the direct mapping (the peer is
// the only recipient); group messages advance to delivered/read only once
// every member has the receipt, mirroring WhatsApp's tick semantics.
// The group membership is passed in rather than looked up: it is the same for
// every id in a receipt, and resolving it per id meant a participant list read
// (and a possible refresh) for each of the hundreds a catch-up receipt names.
func (c *Client) applyParticipantReceipt(ctx context.Context, internalID, participant, kind string, ts time.Time, status string, participants []string, participantsKnown, offlineSync bool) (appstore.Message, bool, error) {
	message, err := c.store.GetMessage(ctx, internalID)
	if err != nil {
		return appstore.Message{}, false, err
	}

	// Receipt rows only matter for our own messages (ticks + message info).
	if message.Direction != appstore.DirectionOutgoing {
		return c.store.UpdateMessageStatus(ctx, internalID, status)
	}

	if err := c.store.UpsertMessageReceipt(ctx, internalID, message.ChatID, participant, kind, ts); err != nil {
		c.log.Warnf("Failed to record receipt for %s from %s: %v", internalID, participant, err)
	} else if !offlineSync {
		// The per-member breakdown changed even when the aggregate status below
		// does not; the `receipts` view keys off this to re-derive live. Skipped
		// during offline sync, matching the status-publish gating in the caller.
		c.daemon.PublishMessageReceipt(message.ChatID, internalID)
	}

	// Membership unknown (or not a group): keep the any-member behavior rather
	// than freezing the ticks at "sent" forever.
	if !participantsKnown {
		return c.store.UpdateMessageStatus(ctx, internalID, status)
	}

	aggregate, err := c.aggregateGroupStatus(ctx, internalID, participants)
	if err != nil {
		return appstore.Message{}, false, err
	}
	if aggregate == "" {
		return message, false, nil
	}
	return c.store.UpdateMessageStatus(ctx, internalID, aggregate)
}

// aggregateGroupStatus computes the WhatsApp group tick state: delivered when
// every member has received the message, read when every member has read it.
// Empty means neither threshold is met yet.
func (c *Client) aggregateGroupStatus(ctx context.Context, internalID string, participants []string) (string, error) {
	receipts, err := c.store.ListMessageReceipts(ctx, internalID)
	if err != nil {
		return "", err
	}
	byJID := make(map[string]appstore.MessageReceipt, len(receipts))
	for _, receipt := range receipts {
		byJID[receipt.ParticipantJID] = receipt
	}

	allDelivered, allRead := true, true
	for _, participant := range participants {
		receipt, ok := byJID[participant]
		if !ok || receipt.DeliveredTs == 0 {
			allDelivered = false
		}
		if !ok || receipt.ReadTs == 0 {
			allRead = false
		}
		if !allDelivered && !allRead {
			return "", nil
		}
	}

	switch {
	case allRead:
		return appstore.StatusRead, nil
	case allDelivered:
		return appstore.StatusDelivered, nil
	default:
		return "", nil
	}
}

// participantReceiptKind maps receipt types that represent another user's
// delivery/read state. Self receipts (our own other devices) and server
// receipts don't describe a recipient and report false.
func participantReceiptKind(receiptType types.ReceiptType) (string, bool) {
	switch receiptType {
	case types.ReceiptTypeDelivered:
		return appstore.ReceiptKindDelivered, true
	case types.ReceiptTypeRead:
		return appstore.ReceiptKindRead, true
	case types.ReceiptTypePlayed:
		return appstore.ReceiptKindPlayed, true
	default:
		return "", false
	}
}

func (c *Client) publishMessageStatusUpdated(ctx context.Context, message appstore.Message) {
	c.daemon.PublishMessageUpdated(toDaemonMessage(message))

	chat, err := c.store.GetChat(ctx, message.ChatID)
	if err != nil {
		c.log.Warnf("Failed to load chat after status update for %s: %v", message.ID, err)
		return
	}
	if chat.LastMessageTime != message.TimestampUnix {
		return
	}

	c.daemon.PublishChatUpdated(toDaemonChat(chat))
}

func (c *Client) MarkChatRead(ctx context.Context, chatID string) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	readCandidates, err := c.store.ReadCandidatesForChat(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}
	c.sendReadReceipts(ctx, chat, chatID, readCandidates)

	updatedChat, err := c.store.MarkMessagesRead(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}

	c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	return updatedChat, nil
}

func (c *Client) MarkChatReadUpTo(ctx context.Context, chatID, upToMessageID string) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	target, err := c.store.GetMessage(ctx, strings.TrimSpace(upToMessageID))
	if err != nil {
		return appstore.Chat{}, err
	}
	if target.ChatID != chatID {
		return appstore.Chat{}, sql.ErrNoRows
	}

	readCandidates, err := c.store.ReadCandidatesForChat(ctx, chatID)
	if err != nil {
		return appstore.Chat{}, err
	}
	bounded := readCandidates[:0]
	internalIDs := make([]string, 0, len(readCandidates))
	for _, candidate := range readCandidates {
		// Same ordering the transcript uses: the stored sort key, then the id.
		if candidate.SortMS < target.SortMS || (candidate.SortMS == target.SortMS && candidate.InternalID <= target.ID) {
			bounded = append(bounded, candidate)
			internalIDs = append(internalIDs, candidate.InternalID)
		}
	}
	// Commit the local read state first, then emit upstream read receipts: a
	// failed store write must not happen after WhatsApp (and other devices) have
	// already been told the messages were read.
	updatedChat, changed, err := c.store.MarkMessagesReadByIDs(ctx, chatID, internalIDs)
	if err != nil {
		return appstore.Chat{}, err
	}
	c.sendReadReceipts(ctx, chat, chatID, bounded)
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

// MarkAllChatsRead marks every unread message read everywhere: local rows
// and badges first, then upstream read receipts per touched chat (the same
// receipts single-chat mark-read sends), then one ChatUpdated per chat.
// Returns the number of chats that had unread.
func (c *Client) MarkAllChatsRead(ctx context.Context) (int, error) {
	type pending struct {
		chatID     string
		jid        types.JID
		candidates []appstore.ReadCandidate
	}
	var pendingChats []pending
	if chats, err := c.store.ListChatsForView(ctx, appstore.ChatListFilter{UnreadOnly: true}); err == nil {
		for _, chat := range chats {
			if chat.UnreadCount <= 0 {
				continue
			}
			jid, err := types.ParseJID(chat.ID)
			if err != nil {
				continue
			}
			candidates, err := c.store.ReadCandidatesForChat(ctx, chat.ID)
			if err != nil || len(candidates) == 0 {
				continue
			}
			pendingChats = append(pendingChats, pending{
				chatID:     chat.ID,
				jid:        c.normalizeJIDForChat(ctx, jid),
				candidates: candidates,
			})
		}
	}
	ids, err := c.store.MarkAllChatsRead(ctx)
	if err != nil {
		return 0, err
	}
	for _, entry := range pendingChats {
		c.sendReadReceipts(ctx, entry.jid, entry.chatID, entry.candidates)
		if chat, err := c.store.GetChat(ctx, entry.chatID); err == nil {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
	return len(ids), nil
}

// MarkMessagePlayed reports that the user listened to an inbound voice note.
// WhatsApp models this as a read receipt with the "played" type, which is what
// turns the sender's mic icon blue. The store flag makes it idempotent: the
// receipt goes out once, however many times the bubble is replayed.
func (c *Client) MarkMessagePlayed(ctx context.Context, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return app.NewCommandError(app.CommandErrorInvalidArgument, "message_id is required")
	}

	message, err := c.store.GetMessage(ctx, messageID)
	if err != nil {
		return err
	}
	if message.MediaKind != appstore.MediaKindVoice {
		return app.NewCommandError(app.CommandErrorRejected, "only voice messages can be marked played")
	}
	// Our own voice notes are played receipts we would be sending to ourselves.
	if message.Direction == appstore.DirectionOutgoing {
		return nil
	}

	updated, flipped, err := c.store.MarkMessageMediaPlayed(ctx, messageID)
	if err != nil {
		return err
	}
	if !flipped {
		return nil
	}
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))

	info, err := mediaRetryMessageInfo(updated)
	if err != nil {
		return err
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		// The local flag stands; the receipt is best-effort, exactly as an
		// ordinary read receipt is when offline.
		return nil
	}
	sender := info.Sender
	if !info.IsGroup {
		sender = types.EmptyJID
	}
	if err := client.MarkRead(ctx, []types.MessageID{info.ID}, time.Now(), info.Chat, sender, types.ReceiptTypePlayed); err != nil {
		c.log.Warnf("Failed to send played receipt for %s: %v", messageID, err)
	}
	return nil
}

func (c *Client) sendReadReceipts(ctx context.Context, chat types.JID, chatID string, readCandidates []appstore.ReadCandidate) {
	if len(readCandidates) == 0 {
		return
	}
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}
	for _, batch := range buildReadBatches(chat, readCandidates) {
		if len(batch.messageIDs) == 0 {
			continue
		}
		if err := client.MarkRead(ctx, batch.messageIDs, time.Now(), chat, batch.sender); err != nil {
			c.log.Warnf("Failed to send read receipt for %s: %v", chatID, err)
		}
	}
}

func (c *Client) SetChatPinned(ctx context.Context, chatID string, pinned bool) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}
	if pinned {
		pinnedCount, err := c.store.PinnedChatCountExcluding(ctx, chatID)
		if err != nil {
			return appstore.Chat{}, err
		}
		if pinnedCount >= maxPinnedChats {
			return appstore.Chat{}, app.NewCommandError(app.CommandErrorRejected, "You can only pin %d chats", maxPinnedChats)
		}
	}
	if err := c.sendRegularLowAppState(ctx, client, appstate.BuildPin(chat, pinned)); err != nil {
		return appstore.Chat{}, err
	}

	order := uint32(0)
	if pinned {
		order = uint32(time.Now().Unix())
	}
	updatedChat, changed, err := c.store.UpdateChatPinState(ctx, chatID, pinned, order)
	if err != nil {
		return appstore.Chat{}, err
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

func (c *Client) SetChatArchived(ctx context.Context, chatID string, archived bool) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}

	// Last-message timestamp/key are optional for BuildArchive; zero values are
	// accepted. WhatsApp auto-unpins an archived chat, so we mirror that locally.
	if err := c.sendRegularLowAppState(ctx, client, appstate.BuildArchive(chat, archived, time.Time{}, nil)); err != nil {
		return appstore.Chat{}, err
	}

	if archived {
		if unpinned, changed, err := c.store.UpdateChatPinState(ctx, chatID, false, 0); err != nil {
			return appstore.Chat{}, err
		} else if changed {
			c.daemon.PublishChatUpdated(toDaemonChat(unpinned))
		}
	}

	updatedChat, changed, err := c.store.UpdateChatArchiveState(ctx, chatID, archived)
	if err != nil {
		return appstore.Chat{}, err
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

// SetChatFavorite is a local-only chat preference; it is not synced to WhatsApp.
func (c *Client) SetChatFavorite(ctx context.Context, chatID string, favorite bool) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	updated, changed, err := c.store.UpdateChatFavoriteState(ctx, c.normalizeJIDForChat(ctx, chat).String(), favorite)
	if err != nil {
		return appstore.Chat{}, err
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updated))
	}
	return updated, nil
}

func (c *Client) CreateChatFolder(ctx context.Context, name string) (appstore.ChatFolder, error) {
	return c.store.CreateChatFolder(ctx, name)
}
func (c *Client) RenameChatFolder(ctx context.Context, id int64, name string) error {
	return c.store.RenameChatFolder(ctx, id, name)
}
func (c *Client) DeleteChatFolder(ctx context.Context, id int64) error {
	return c.store.DeleteChatFolder(ctx, id)
}
func (c *Client) SetChatFolder(ctx context.Context, chatID string, folderID *int64) error {
	if err := c.store.SetChatFolder(ctx, chatID, folderID); err != nil {
		return err
	}
	return nil
}

// SetChatMuted mutes or unmutes a chat and syncs it to the device. A zero
// duration with muted=true means "forever" (stored as -1); otherwise the chat
// stays muted until now+duration. Muting uses the regular_high app-state
// collection, mirroring message starring.
func (c *Client) SetChatMuted(ctx context.Context, chatID string, muted bool, duration time.Duration) (appstore.Chat, error) {
	chat, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	chat = c.normalizeJIDForChat(ctx, chat)
	chatID = chat.String()

	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Chat{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}

	if err := c.sendRegularHighAppState(ctx, client, appstate.BuildMute(chat, muted, duration)); err != nil {
		return appstore.Chat{}, err
	}

	var muteEnd int64
	if muted {
		if duration > 0 {
			muteEnd = time.Now().Add(duration).UnixMilli()
		} else {
			muteEnd = -1
		}
	}

	updatedChat, changed, err := c.store.UpdateChatMuteState(ctx, chatID, muted, muteEnd)
	if err != nil {
		return appstore.Chat{}, err
	}
	if changed {
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}
	return updatedChat, nil
}

func (c *Client) sendRegularLowAppState(ctx context.Context, client *whatsmeow.Client, patch appstate.PatchInfo) error {
	c.appStateMu.Lock()
	defer c.appStateMu.Unlock()

	if err := client.SendAppState(ctx, patch); err != nil {
		if !isAppStateConflictError(err) {
			return err
		}
		c.log.Warnf("WhatsApp app state conflict while updating pins; resyncing regular_low and retrying: %v", err)
		if _, syncErr := fetchFullRegularLowAppState(ctx, client); syncErr != nil {
			return app.NewCommandError(app.CommandErrorRejected, "WhatsApp sync conflict. Try again in a moment.")
		}
		if retryErr := client.SendAppState(ctx, patch); retryErr != nil {
			return app.NewCommandError(app.CommandErrorRejected, "WhatsApp sync conflict. Try again in a moment.")
		}
	}
	return nil
}

func isAppStateConflictError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, appstate.ErrMismatchingLTHash) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, `code="409"`) ||
		strings.Contains(message, "mismatching LTHash") ||
		strings.Contains(message, "failed to verify patch")
}

func receiptStatus(receiptType types.ReceiptType) (string, bool) {
	switch receiptType {
	case types.ReceiptTypeDelivered:
		return appstore.StatusDelivered, true
	case types.ReceiptTypeSender:
		return appstore.StatusSent, true
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf, types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return appstore.StatusRead, true
	case types.ReceiptTypeServerError:
		return appstore.StatusFailed, true
	default:
		return "", false
	}
}

func buildReadBatches(chat types.JID, candidates []appstore.ReadCandidate) []readBatch {
	grouped := make(map[string]*readBatch)
	order := make([]string, 0)

	for _, candidate := range candidates {
		sender, err := senderForReadReceipt(chat, candidate.SenderID)
		if err != nil {
			continue
		}

		key := sender.String()
		batch, ok := grouped[key]
		if !ok {
			batch = &readBatch{sender: sender, messageIDs: make([]types.MessageID, 0, 8)}
			grouped[key] = batch
			order = append(order, key)
		}

		batch.messageIDs = append(batch.messageIDs, types.MessageID(candidate.ExternalID))
	}

	batches := make([]readBatch, 0, len(order))
	for _, key := range order {
		batches = append(batches, *grouped[key])
	}

	return batches
}

func senderForReadReceipt(chat types.JID, senderID string) (types.JID, error) {
	if chat.Server == types.GroupServer || chat.Server == types.BroadcastServer {
		return types.ParseJID(senderID)
	}

	if senderID != "" && senderID != "me" {
		return types.ParseJID(senderID)
	}

	return chat, nil
}

func internalMessageIDForChat(chatID string, messageID types.MessageID) string {
	return fmt.Sprintf("%s:%s", chatID, messageID)
}
