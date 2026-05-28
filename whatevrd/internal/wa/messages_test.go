package wa

import (
	"testing"
	"time"

	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"

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

func TestFormatPhoneDisplayNameFormatsInternationalNumber(t *testing.T) {
	jid := types.NewJID("917060029183", types.DefaultUserServer)
	if got, want := formatPhoneDisplayName(jid), "+91 70600 29183"; got != want {
		t.Fatalf("formatPhoneDisplayName() = %q, want %q", got, want)
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
