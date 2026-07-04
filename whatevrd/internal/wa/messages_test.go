package wa

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func TestMapWebMessageStatusKnownValues(t *testing.T) {
	cases := []struct {
		in   waWeb.WebMessageInfo_Status
		want string
	}{
		{waWeb.WebMessageInfo_PENDING, appstore.StatusPending},
		{waWeb.WebMessageInfo_SERVER_ACK, appstore.StatusSent},
		{waWeb.WebMessageInfo_DELIVERY_ACK, appstore.StatusDelivered},
		{waWeb.WebMessageInfo_READ, appstore.StatusRead},
		{waWeb.WebMessageInfo_PLAYED, appstore.StatusRead},
		{waWeb.WebMessageInfo_ERROR, appstore.StatusFailed},
	}

	for _, tc := range cases {
		status := tc.in
		webMsg := &waWeb.WebMessageInfo{Status: &status}
		got := mapWebMessageStatus(webMsg)
		if got != tc.want {
			t.Errorf("mapWebMessageStatus(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMapWebMessageStatusReturnsEmptyForNil(t *testing.T) {
	if got := mapWebMessageStatus(nil); got != "" {
		t.Errorf("mapWebMessageStatus(nil) = %q, want empty", got)
	}

	if got := mapWebMessageStatus(&waWeb.WebMessageInfo{}); got != "" {
		t.Errorf("mapWebMessageStatus(empty) = %q, want empty", got)
	}
}

func TestReceiptStatusSenderIsSentOnly(t *testing.T) {
	cases := []struct {
		name string
		in   types.ReceiptType
		want string
	}{
		{"delivered", types.ReceiptTypeDelivered, appstore.StatusDelivered},
		{"sender", types.ReceiptTypeSender, appstore.StatusSent},
		{"read", types.ReceiptTypeRead, appstore.StatusRead},
		{"played", types.ReceiptTypePlayed, appstore.StatusRead},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := receiptStatus(tc.in)
			if !ok {
				t.Fatalf("receiptStatus(%v) returned ok=false", tc.in)
			}
			if got != tc.want {
				t.Fatalf("receiptStatus(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReceiptStatusIgnoresUnsupportedTypes(t *testing.T) {
	if got, ok := receiptStatus(types.ReceiptTypeInactive); ok || got != "" {
		t.Fatalf("receiptStatus(inactive) = %q, %v; want empty, false", got, ok)
	}
}

func TestToDaemonChatPreservesLastMessageStatus(t *testing.T) {
	chat := appstore.Chat{
		ID:                   "chat-1",
		Name:                 "Chat One",
		LastMessage:          "hello",
		LastMessageTime:      123,
		LastMessageDirection: appstore.DirectionOutgoing,
		LastMessageStatus:    appstore.StatusDelivered,
		UnreadCount:          2,
		IsGroup:              true,
		AvatarLocalPath:      "/tmp/avatar.jpg",
	}

	got := toDaemonChat(chat)
	if got.LastMessageDirection != chat.LastMessageDirection {
		t.Fatalf("LastMessageDirection = %q, want %q", got.LastMessageDirection, chat.LastMessageDirection)
	}
	if got.LastMessageStatus != chat.LastMessageStatus {
		t.Fatalf("LastMessageStatus = %q, want %q", got.LastMessageStatus, chat.LastMessageStatus)
	}
}

func TestHistorySyncTypeMapping(t *testing.T) {
	cases := []struct {
		in   waHistorySync.HistorySync_HistorySyncType
		want app.HistorySyncType
	}{
		{waHistorySync.HistorySync_INITIAL_BOOTSTRAP, app.HistorySyncTypeInitialBootstrap},
		{waHistorySync.HistorySync_INITIAL_STATUS_V3, app.HistorySyncTypeInitialStatusV3},
		{waHistorySync.HistorySync_FULL, app.HistorySyncTypeFull},
		{waHistorySync.HistorySync_RECENT, app.HistorySyncTypeRecent},
		{waHistorySync.HistorySync_PUSH_NAME, app.HistorySyncTypePushName},
		{waHistorySync.HistorySync_NON_BLOCKING_DATA, app.HistorySyncTypeNonBlockingData},
		{waHistorySync.HistorySync_ON_DEMAND, app.HistorySyncTypeOnDemand},
	}

	for _, tc := range cases {
		if got := historySyncType(tc.in); got != tc.want {
			t.Errorf("historySyncType(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHistorySyncIsCompleteRespectsTerminalTypes(t *testing.T) {
	if !historySyncIsComplete(app.HistorySyncTypePushName, 0) {
		t.Error("PUSH_NAME should always be complete (no progress field)")
	}
	if !historySyncIsComplete(app.HistorySyncTypeNonBlockingData, 0) {
		t.Error("NON_BLOCKING_DATA should always be complete (no progress field)")
	}
	if historySyncIsComplete(app.HistorySyncTypeFull, 50) {
		t.Error("FULL at 50% should not be complete")
	}
	if !historySyncIsComplete(app.HistorySyncTypeFull, 100) {
		t.Error("FULL at 100% should be complete")
	}
	if !historySyncIsComplete(app.HistorySyncTypeRecent, 100) {
		t.Error("RECENT at 100% should be complete")
	}
}

func TestEffectiveHistorySyncUnreadMirrorsMarkedUnreadDot(t *testing.T) {
	markedUnread := true
	if got := effectiveHistorySyncUnread(&waHistorySync.Conversation{MarkedAsUnread: &markedUnread}); got != 1 {
		t.Fatalf("effective unread for marked-unread chat = %d, want 1", got)
	}

	unread := uint32(4)
	if got := effectiveHistorySyncUnread(&waHistorySync.Conversation{UnreadCount: &unread, MarkedAsUnread: &markedUnread}); got != 4 {
		t.Fatalf("effective unread with real count = %d, want 4", got)
	}
}

func TestHistorySyncMarkedUnreadPublishesChatUpdated(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	daemon := app.NewDaemon(app.Paths{})
	events, unsubscribe := daemon.SubscribeDaemonEvents()
	t.Cleanup(unsubscribe)

	client := &Client{
		store:  db,
		daemon: daemon,
		log:    waLog.Noop,
		client: &whatsmeow.Client{Store: &waStore.Device{}},
	}
	markedUnread := true
	syncType := waHistorySync.HistorySync_RECENT
	chatID := types.NewJID("12345", types.DefaultUserServer).String()
	client.processHistorySyncData(ctx, &waHistorySync.HistorySync{
		SyncType: &syncType,
		Conversations: []*waHistorySync.Conversation{{
			ID:             proto.String(chatID),
			MarkedAsUnread: &markedUnread,
		}},
	})

	chat, err := db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("stored unread = %d, want 1", chat.UnreadCount)
	}

	for deadline := time.After(time.Second); ; {
		select {
		case evt := <-events:
			if evt.Kind == app.DaemonEventChatUpdated && evt.Chat.ID == chatID && evt.Chat.UnreadCount == 1 {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for marked-unread ChatUpdated")
		}
	}
}

func TestHistorySyncPreservesPinWhenPinnedFieldAbsent(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	chatID := types.NewJID("12345", types.DefaultUserServer).String()
	if _, err := db.EnsureChat(ctx, chatID, chatID, false); err != nil {
		t.Fatalf("ensure chat: %v", err)
	}
	if _, _, err := db.UpdateChatPinState(ctx, chatID, true, 123); err != nil {
		t.Fatalf("pin chat: %v", err)
	}

	client := &Client{
		store:  db,
		daemon: app.NewDaemon(app.Paths{}),
		log:    waLog.Noop,
		client: &whatsmeow.Client{Store: &waStore.Device{}},
	}
	syncType := waHistorySync.HistorySync_RECENT
	client.processHistorySyncData(ctx, &waHistorySync.HistorySync{
		SyncType: &syncType,
		Conversations: []*waHistorySync.Conversation{{
			ID: proto.String(chatID),
		}},
	})

	chat, err := db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat after absent pin: %v", err)
	}
	if !chat.IsPinned || chat.PinnedOrder != 123 {
		t.Fatalf("pin changed after absent field: pinned=%v order=%d, want true/123", chat.IsPinned, chat.PinnedOrder)
	}

	explicitUnpin := uint32(0)
	client.processHistorySyncData(ctx, &waHistorySync.HistorySync{
		SyncType: &syncType,
		Conversations: []*waHistorySync.Conversation{{
			ID:     proto.String(chatID),
			Pinned: &explicitUnpin,
		}},
	})

	chat, err = db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat after explicit unpin: %v", err)
	}
	if chat.IsPinned || chat.PinnedOrder != 0 {
		t.Fatalf("explicit unpin not applied: pinned=%v order=%d, want false/0", chat.IsPinned, chat.PinnedOrder)
	}
}

func TestNotificationTimestampFreshAllowsRecentAndUnknownTimestamps(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	if !notificationTimestampFresh(0, now) {
		t.Fatal("zero timestamp should be treated as fresh")
	}
	if !notificationTimestampFresh(now.Add(-liveNotificationMaxAge).Unix(), now) {
		t.Fatal("timestamp at max age should be treated as fresh")
	}
	if !notificationTimestampFresh(now.Add(30*time.Second).Unix(), now) {
		t.Fatal("future timestamp should be treated as fresh")
	}
}

func TestNotificationTimestampFreshSuppressesStaleReconnectBacklog(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	stale := now.Add(-liveNotificationMaxAge - time.Second).Unix()

	if notificationTimestampFresh(stale, now) {
		t.Fatal("stale reconnect backlog timestamp should not be fresh")
	}
}

func TestMessageTimestampPrefersOutgoingHistoryC2STimestamp(t *testing.T) {
	c2s := uint64(1_700_000_100)
	info := types.MessageInfo{
		MessageSource: types.MessageSource{IsFromMe: true},
		Timestamp:     time.Unix(1_700_000_200, 0),
	}
	webMsg := &waWeb.WebMessageInfo{MessageC2STimestamp: &c2s}

	got := messageTimestamp(info, ingestOptions{source: sourceHistorySync}, webMsg)
	if want := time.Unix(1_700_000_100, 0); !got.Equal(want) {
		t.Fatalf("messageTimestamp() = %v, want %v", got, want)
	}
}

func TestMessageTimestampParsesMillisecondC2STimestamp(t *testing.T) {
	c2s := uint64(1_700_000_100_123)
	info := types.MessageInfo{
		MessageSource: types.MessageSource{IsFromMe: true},
		Timestamp:     time.Unix(1_700_000_200, 0),
	}
	webMsg := &waWeb.WebMessageInfo{MessageC2STimestamp: &c2s}

	got := messageTimestamp(info, ingestOptions{source: sourceHistorySync}, webMsg)
	if want := time.UnixMilli(1_700_000_100_123); !got.Equal(want) {
		t.Fatalf("messageTimestamp() = %v, want %v", got, want)
	}
}

func TestMessageTimestampFallsBackForIncomingLiveAndInvalidC2S(t *testing.T) {
	fallback := time.Unix(1_700_000_200, 0)
	c2s := uint64(1_700_000_100)
	invalidC2S := uint64(9_999_999_999_999)

	cases := []struct {
		name   string
		info   types.MessageInfo
		opts   ingestOptions
		webMsg *waWeb.WebMessageInfo
	}{
		{
			name:   "incoming history",
			info:   types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: false}, Timestamp: fallback},
			opts:   ingestOptions{source: sourceHistorySync},
			webMsg: &waWeb.WebMessageInfo{MessageC2STimestamp: &c2s},
		},
		{
			name:   "live outgoing",
			info:   types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: true}, Timestamp: fallback},
			opts:   ingestOptions{source: sourceLive},
			webMsg: &waWeb.WebMessageInfo{MessageC2STimestamp: &c2s},
		},
		{
			name:   "invalid c2s",
			info:   types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: true}, Timestamp: fallback},
			opts:   ingestOptions{source: sourceHistorySync},
			webMsg: &waWeb.WebMessageInfo{MessageC2STimestamp: &invalidC2S},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageTimestamp(tc.info, tc.opts, tc.webMsg); !got.Equal(fallback) {
				t.Fatalf("messageTimestamp() = %v, want fallback %v", got, fallback)
			}
		})
	}
}

func TestMessageTimestampPrefersRetryTimestampOverride(t *testing.T) {
	fallback := time.Unix(1_700_000_200, 0)
	override := time.Unix(1_700_000_100, 0)
	c2s := uint64(1_700_000_050)
	info := types.MessageInfo{
		MessageSource: types.MessageSource{IsFromMe: true},
		Timestamp:     fallback,
	}
	webMsg := &waWeb.WebMessageInfo{MessageC2STimestamp: &c2s}

	got := messageTimestamp(info, ingestOptions{source: sourceHistorySync, timestampOverride: override}, webMsg)
	if !got.Equal(override) {
		t.Fatalf("messageTimestamp() = %v, want override %v", got, override)
	}
}

func TestQuotedReplyPreviewExtractsImageCaption(t *testing.T) {
	text, mediaKind, mimeType := quotedReplyPreview(&waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			Caption:  proto.String("caption"),
			Mimetype: proto.String("image/png"),
		},
	})
	if text != "caption" || mediaKind != appstore.MediaKindImage || mimeType != "image/png" {
		t.Fatalf("quotedReplyPreview() = %q, %q, %q", text, mediaKind, mimeType)
	}
}

func TestOutgoingReplyContextInfoUsesExternalIDAndParticipant(t *testing.T) {
	ctx := context.Background()
	db, err := appstore.Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveTextMessage(ctx, appstore.TextMessageInput{
		ID:        "chat-1:quoted",
		ChatID:    "chat-1",
		SenderID:  "111@s.whatsapp.net",
		Text:      "quoted",
		Timestamp: time.Unix(100, 0),
		Direction: appstore.DirectionIncoming,
		Status:    appstore.StatusDelivered,
	}); err != nil {
		t.Fatalf("save quoted message: %v", err)
	}

	client := &Client{store: db}
	message := appstore.Message{
		ID:     "chat-1:new",
		ChatID: "chat-1",
		Text:   "reply",
		ReplyTo: appstore.MessageReply{
			MessageID: "chat-1:quoted",
			SenderID:  "111@s.whatsapp.net",
			Text:      "quoted",
		},
	}
	contextInfo := client.outgoingReplyContextInfo(ctx, nil, message)
	if contextInfo == nil {
		t.Fatal("expected context info")
	}
	if contextInfo.GetStanzaID() != "quoted" {
		t.Fatalf("stanza ID = %q, want quoted", contextInfo.GetStanzaID())
	}
	if contextInfo.GetParticipant() != "111@s.whatsapp.net" {
		t.Fatalf("participant = %q", contextInfo.GetParticipant())
	}
	if contextInfo.RemoteJID != nil {
		t.Fatalf("remote JID = %q, want unset for same-chat reply", contextInfo.GetRemoteJID())
	}
	if contextInfo.GetQuotedMessage().GetConversation() != "quoted" {
		t.Fatalf("quoted message = %+v", contextInfo.GetQuotedMessage())
	}
}

func TestQuotedMessageFromStoredPreservesStickerPayload(t *testing.T) {
	original := &waE2E.StickerMessage{
		Mimetype:      proto.String("image/webp"),
		PngThumbnail:  []byte{0x89, 0x50, 0x4e, 0x47},
		URL:           proto.String("https://example/sticker.enc"),
		FileEncSHA256: []byte{0x01, 0x02, 0x03},
	}
	payload, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("marshal sticker: %v", err)
	}

	quoted := quotedMessageFromStored(appstore.Message{
		MediaKind:     appstore.MediaKindSticker,
		MediaMimeType: "image/webp",
		MediaPayload:  payload,
	})
	sticker := quoted.GetStickerMessage()
	if sticker == nil {
		t.Fatal("expected sticker message in quote")
	}
	if string(sticker.GetPngThumbnail()) != string(original.GetPngThumbnail()) {
		t.Fatalf("thumbnail not preserved: %x", sticker.GetPngThumbnail())
	}
	if sticker.GetURL() != original.GetURL() || string(sticker.GetFileEncSHA256()) != string(original.GetFileEncSHA256()) {
		t.Fatalf("media keys not preserved: %+v", sticker)
	}
}

func TestQuotedMessageFromStoredPreservesImageThumbnail(t *testing.T) {
	original := &waE2E.ImageMessage{
		Mimetype:      proto.String("image/jpeg"),
		Caption:       proto.String("hi"),
		JPEGThumbnail: []byte{0xff, 0xd8, 0xff},
		MediaKey:      []byte{0x09, 0x08},
	}
	payload, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("marshal image: %v", err)
	}

	quoted := quotedMessageFromStored(appstore.Message{
		MediaKind:     appstore.MediaKindImage,
		MediaMimeType: "image/jpeg",
		Text:          "hi",
		MediaPayload:  payload,
	})
	img := quoted.GetImageMessage()
	if img == nil {
		t.Fatal("expected image message in quote")
	}
	if string(img.GetJPEGThumbnail()) != string(original.GetJPEGThumbnail()) {
		t.Fatalf("thumbnail not preserved: %x", img.GetJPEGThumbnail())
	}
	if img.GetCaption() != "hi" {
		t.Fatalf("caption = %q", img.GetCaption())
	}
}

func TestOutgoingReplyParticipantNormalizesLIDToPNForDM(t *testing.T) {
	ctx := context.Background()
	client := &Client{}

	// No live whatsmeow client, so a LID can't be resolved; it should be left
	// as-is rather than crash. A PN sender in a DM is returned unchanged.
	pn := "111@s.whatsapp.net"
	if got := client.outgoingReplyParticipant(ctx, nil, "111@s.whatsapp.net", pn); got != pn {
		t.Fatalf("participant = %q, want %q", got, pn)
	}

	lid := "55555@lid"
	if got := client.outgoingReplyParticipant(ctx, nil, "111@s.whatsapp.net", lid); got != lid {
		t.Fatalf("unresolvable LID participant = %q, want %q", got, lid)
	}
}

func TestOutgoingImageThumbnailDownscales(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x40, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode source: %v", err)
	}

	thumb := outgoingImageThumbnail(buf.Bytes())
	if len(thumb) == 0 {
		t.Fatal("expected a thumbnail")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if cfg.Width > outgoingThumbnailMaxDimension || cfg.Height > outgoingThumbnailMaxDimension {
		t.Fatalf("thumbnail %dx%d exceeds max %d", cfg.Width, cfg.Height, outgoingThumbnailMaxDimension)
	}
	if cfg.Width != outgoingThumbnailMaxDimension {
		t.Fatalf("expected longest side %d, got width %d", outgoingThumbnailMaxDimension, cfg.Width)
	}
}

func TestFormatPhoneDisplayNameFormatsInternationalNumber(t *testing.T) {
	jid := types.NewJID("917060029183", types.DefaultUserServer)
	if got, want := formatPhoneDisplayName(jid), "+91 70600 29183"; got != want {
		t.Fatalf("formatPhoneDisplayName() = %q, want %q", got, want)
	}
}

func TestDisplayNameForChatUsesPhoneFallbackForOneToOne(t *testing.T) {
	client := &Client{}
	jid := types.NewJID("917060029183", types.DefaultUserServer)

	name, source := client.displayNameForChat(context.Background(), jid, false, "", "")
	if name != "+91 70600 29183" || source != appstore.ChatNameSourcePhone {
		t.Fatalf("displayNameForChat() = %q, %q; want formatted phone source", name, source)
	}
}

func TestFormatPhoneDisplayNameRejectsLID(t *testing.T) {
	jid := types.NewJID("123456", types.HiddenUserServer)
	if got := formatPhoneDisplayName(jid); got != "" {
		t.Fatalf("formatPhoneDisplayName(lid) = %q, want empty", got)
	}
}

func TestWhatsAppDisplayNamePrefixesName(t *testing.T) {
	if got := whatsAppDisplayName(" Alice "); got != "~Alice" {
		t.Fatalf("whatsAppDisplayName() = %q, want ~Alice", got)
	}
}

func TestWhatsAppDisplayNameDoesNotDoublePrefix(t *testing.T) {
	if got := whatsAppDisplayName("~Alice"); got != "~Alice" {
		t.Fatalf("whatsAppDisplayName() = %q, want ~Alice", got)
	}
}

func TestUnsupportedMessageLabel(t *testing.T) {
	cases := []struct {
		name   string
		evt    *events.Message
		want   string
		wantOK bool
	}{
		{
			name: "document with filename",
			evt: &events.Message{Message: &waE2E.Message{
				DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("report.pdf")},
			}},
			want:   "Document: report.pdf",
			wantOK: true,
		},
		{
			name: "captioned document",
			evt: &events.Message{Message: &waE2E.Message{
				DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("a.pdf"), Caption: proto.String("see this")},
			}},
			want:   "Document: a.pdf — see this",
			wantOK: true,
		},
		{
			name: "voice note",
			evt: &events.Message{Message: &waE2E.Message{
				AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)},
			}},
			want:   "Voice message",
			wantOK: true,
		},
		{
			name: "audio file",
			evt: &events.Message{Message: &waE2E.Message{
				AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(false)},
			}},
			want:   "Audio",
			wantOK: true,
		},
		{
			name: "gif playback video",
			evt: &events.Message{Message: &waE2E.Message{
				VideoMessage: &waE2E.VideoMessage{GifPlayback: proto.Bool(true)},
			}},
			want:   "GIF",
			wantOK: true,
		},
		{
			name: "poll v3",
			evt: &events.Message{Message: &waE2E.Message{
				PollCreationMessageV3: &waE2E.PollCreationMessage{Name: proto.String("Lunch?")},
			}},
			want:   "Poll: Lunch?",
			wantOK: true,
		},
		{
			name: "view once photo",
			evt: &events.Message{
				IsViewOnce: true,
				Message: &waE2E.Message{
					ImageMessage: &waE2E.ImageMessage{},
				},
			},
			want:   "View once photo",
			wantOK: true,
		},
		{
			name: "location with name",
			evt: &events.Message{Message: &waE2E.Message{
				LocationMessage: &waE2E.LocationMessage{Name: proto.String("Cafe")},
			}},
			want:   "Location: Cafe",
			wantOK: true,
		},
		{
			name: "poll update stays invisible",
			evt: &events.Message{Message: &waE2E.Message{
				PollUpdateMessage: &waE2E.PollUpdateMessage{},
			}},
			wantOK: false,
		},
		{
			name: "protocol message stays invisible",
			evt: &events.Message{Message: &waE2E.Message{
				ProtocolMessage: &waE2E.ProtocolMessage{},
			}},
			wantOK: false,
		},
		{
			name:   "empty message stays invisible",
			evt:    &events.Message{Message: &waE2E.Message{}},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := unsupportedMessageLabel(tc.evt)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("unsupportedMessageLabel() = %q, %v; want %q, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
