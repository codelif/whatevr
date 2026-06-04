package wa

import (
	"context"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
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
	contextInfo := outgoingReplyContextInfo(nil, message)
	if contextInfo == nil {
		t.Fatal("expected context info")
	}
	if contextInfo.GetStanzaID() != "quoted" {
		t.Fatalf("stanza ID = %q, want quoted", contextInfo.GetStanzaID())
	}
	if contextInfo.GetParticipant() != "111@s.whatsapp.net" {
		t.Fatalf("participant = %q", contextInfo.GetParticipant())
	}
	if contextInfo.GetQuotedMessage().GetConversation() != "quoted" {
		t.Fatalf("quoted message = %+v", contextInfo.GetQuotedMessage())
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
