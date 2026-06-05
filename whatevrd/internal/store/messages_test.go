package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSaveTextMessageInsertsOnceAndUpdatesChat(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	input := TextMessageInput{
		ID:          "chat-1:msg-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}

	first, err := db.SaveTextMessage(ctx, input)
	if err != nil {
		t.Fatalf("save first message: %v", err)
	}
	if !first.Inserted {
		t.Fatal("expected first save to insert")
	}
	if first.Chat.LastMessage != "hello" || first.Chat.LastMessageTime != 100 || first.Chat.UnreadCount != 1 {
		t.Fatalf("unexpected chat after first save: %+v", first.Chat)
	}

	second, err := db.SaveTextMessage(ctx, input)
	if err != nil {
		t.Fatalf("save duplicate message: %v", err)
	}
	if second.Inserted {
		t.Fatal("expected duplicate save to be ignored")
	}
	if second.Chat.UnreadCount != 1 {
		t.Fatalf("duplicate changed unread count: %+v", second.Chat)
	}
}

func TestSaveTextMessageDoesNotUnreadOutgoingMessages(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	saved, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        "chat-1:msg-2",
		ChatID:    "chat-1",
		ChatName:  "Test Chat",
		SenderID:  "me",
		Text:      "from another linked device",
		Timestamp: time.Unix(200, 0),
		Direction: DirectionOutgoing,
		Status:    StatusSent,
	})
	if err != nil {
		t.Fatalf("save outgoing message: %v", err)
	}
	if saved.Chat.UnreadCount != 0 {
		t.Fatalf("outgoing message changed unread count: %+v", saved.Chat)
	}
}

func TestSaveTextMessagePersistsReplyMetadata(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "chat-1"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        chatID + ":original",
		ChatID:    chatID,
		SenderID:  "sender-1",
		Text:      "original text",
		Timestamp: time.Unix(100, 0),
		Direction: DirectionIncoming,
		Status:    StatusDelivered,
	}); err != nil {
		t.Fatalf("save original message: %v", err)
	}

	saved, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        chatID + ":reply",
		ChatID:    chatID,
		SenderID:  "me",
		Text:      "reply text",
		Timestamp: time.Unix(200, 0),
		Direction: DirectionOutgoing,
		Status:    StatusPending,
		ReplyTo: MessageReply{
			MessageID:     chatID + ":original",
			SenderID:      "sender-1",
			SenderName:    "Alice",
			Text:          "original text",
			MediaKind:     MediaKindImage,
			MediaMimeType: "image/jpeg",
			Direction:     DirectionIncoming,
		},
	})
	if err != nil {
		t.Fatalf("save reply message: %v", err)
	}
	if saved.Message.ReplyTo.MessageID != chatID+":original" || saved.Message.ReplyTo.SenderName != "Alice" || saved.Message.ReplyTo.MediaKind != MediaKindImage {
		t.Fatalf("saved reply metadata = %+v", saved.Message.ReplyTo)
	}

	messages, err := db.ListMessages(ctx, chatID, 10, "")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[1].ReplyTo.MessageID != chatID+":original" || messages[1].ReplyTo.Text != "original text" {
		t.Fatalf("listed reply metadata = %+v", messages[1].ReplyTo)
	}

	pending, err := db.ListPendingOutgoingMessages(ctx, 10, time.Now())
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ReplyTo.MessageID != chatID+":original" {
		t.Fatalf("pending reply metadata = %+v", pending)
	}
}

func TestSaveTextMessageDoesNotRegressChatSummary(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	latest, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:latest",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "latest",
		Timestamp:   time.Unix(200, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save latest message: %v", err)
	}
	if latest.Chat.LastMessage != "latest" {
		t.Fatalf("unexpected latest chat summary: %+v", latest.Chat)
	}

	older, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:older",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "older",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save older message: %v", err)
	}
	if older.Chat.LastMessage != "latest" || older.Chat.LastMessageTime != 200 {
		t.Fatalf("older message regressed chat summary: %+v", older.Chat)
	}
}

func TestRecordUndecryptableMessageTimestampKeepsEarliest(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.RecordUndecryptableMessageTimestamp(ctx, "chat-1:msg-1", "chat-1", "msg-1", "sender-1", time.Unix(200, 0)); err != nil {
		t.Fatalf("record later timestamp: %v", err)
	}
	if _, err := db.RecordUndecryptableMessageTimestamp(ctx, "chat-1:msg-1", "chat-1", "msg-1", "sender-1", time.Unix(100, 0)); err != nil {
		t.Fatalf("record earlier timestamp: %v", err)
	}
	if _, err := db.RecordUndecryptableMessageTimestamp(ctx, "chat-1:msg-1", "chat-1", "msg-1", "sender-1", time.Unix(300, 0)); err != nil {
		t.Fatalf("record latest timestamp: %v", err)
	}

	timestamp, ok, err := db.LookupUndecryptableMessageTimestamp(ctx, "chat-1:msg-1")
	if err != nil {
		t.Fatalf("lookup timestamp: %v", err)
	}
	if !ok || timestamp.Unix() != 100 {
		t.Fatalf("lookup = %v, %t, want unix 100", timestamp, ok)
	}
}

func TestUpdateMessageMediaLocalPathWithDimensionsPersistsDimensions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveMediaMessage(ctx, MediaMessageInput{
		TextMessageInput: TextMessageInput{
			ID:        "chat-1:image-1",
			ChatID:    "chat-1",
			ChatName:  "Test Chat",
			SenderID:  "sender-1",
			Timestamp: time.Unix(100, 0),
		},
		MediaKind:     MediaKindImage,
		MediaMimeType: "image/jpeg",
		MediaWidth:    4,
		MediaHeight:   3,
	}); err != nil {
		t.Fatalf("save image message: %v", err)
	}

	updated, err := db.UpdateMessageMediaLocalPathWithDimensions(ctx, "chat-1:image-1", "/tmp/image.jpg", 1200, 240)
	if err != nil {
		t.Fatalf("UpdateMessageMediaLocalPathWithDimensions() error = %v", err)
	}
	if updated.MediaLocalPath != "/tmp/image.jpg" || updated.MediaWidth != 1200 || updated.MediaHeight != 240 {
		t.Fatalf("updated media = path %q dimensions %dx%d, want path and 1200x240", updated.MediaLocalPath, updated.MediaWidth, updated.MediaHeight)
	}

	loaded, err := db.GetMessage(ctx, "chat-1:image-1")
	if err != nil {
		t.Fatalf("GetMessage() error = %v", err)
	}
	if loaded.MediaLocalPath != "/tmp/image.jpg" || loaded.MediaWidth != 1200 || loaded.MediaHeight != 240 {
		t.Fatalf("stored media = path %q dimensions %dx%d, want path and 1200x240", loaded.MediaLocalPath, loaded.MediaWidth, loaded.MediaHeight)
	}
}

func TestRecordUndecryptableMessageTimestampCorrectsExistingMessageAndChatSummary(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        "chat-1:newer",
		ChatID:    "chat-1",
		ChatName:  "Test Chat",
		SenderID:  "sender-1",
		Text:      "newer",
		Timestamp: time.Unix(300, 0),
		Direction: DirectionIncoming,
		Status:    StatusDelivered,
	}); err != nil {
		t.Fatalf("save newer message: %v", err)
	}
	if _, err := db.RecordUndecryptableMessageTimestamp(ctx, "chat-1:image", "chat-1", "image", "sender-2", time.Unix(200, 0)); err != nil {
		t.Fatalf("record original undecryptable timestamp: %v", err)
	}
	if _, err := db.SaveMediaMessage(ctx, MediaMessageInput{
		TextMessageInput: TextMessageInput{
			ID:        "chat-1:image",
			ChatID:    "chat-1",
			ChatName:  "Test Chat",
			SenderID:  "sender-2",
			Timestamp: time.Unix(400, 0),
			Direction: DirectionIncoming,
			Status:    StatusDelivered,
		},
		MediaKind:     MediaKindImage,
		MediaMimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("save image message: %v", err)
	}
	chat, err := db.GetChat(ctx, "chat-1")
	if err != nil {
		t.Fatalf("get chat before correction: %v", err)
	}
	if chat.LastMessage != "[Image]" || chat.LastMessageTime != 400 {
		t.Fatalf("chat before correction = %+v, want image at 400", chat)
	}

	correction, err := db.RecordUndecryptableMessageTimestamp(ctx, "chat-1:image", "chat-1", "image", "sender-2", time.Unix(300, 0))
	if err != nil {
		t.Fatalf("record undecryptable timestamp: %v", err)
	}
	if !correction.Changed {
		t.Fatal("expected timestamp correction")
	}
	if correction.Message.TimestampUnix != 200 {
		t.Fatalf("corrected timestamp = %d, want 200", correction.Message.TimestampUnix)
	}
	if correction.Chat.LastMessage != "newer" || correction.Chat.LastMessageTime != 300 {
		t.Fatalf("corrected chat = %+v, want newer at 300", correction.Chat)
	}

	message, err := db.GetMessage(ctx, "chat-1:image")
	if err != nil {
		t.Fatalf("get corrected message: %v", err)
	}
	if message.TimestampUnix != 200 {
		t.Fatalf("stored timestamp = %d, want 200", message.TimestampUnix)
	}
}

func TestListMessagesIncludesSenderProfile(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "1234567890@g.us"
	senderID := "917060029183@s.whatsapp.net"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:         chatID + ":msg-1",
		ChatID:     chatID,
		SenderID:   senderID,
		SenderName: "Alice",
		Text:       "hello",
		Timestamp:  time.Unix(100, 0),
		Direction:  DirectionIncoming,
		Status:     StatusDelivered,
		IsGroup:    true,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}

	messages, err := db.ListMessages(ctx, chatID, 10, "")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].SenderName != "Alice" {
		t.Fatalf("sender name = %q, want Alice", messages[0].SenderName)
	}

	if err := db.UpdateSenderAvatar(ctx, senderID, "pic-1", "/tmp/alice.jpg"); err != nil {
		t.Fatalf("update sender avatar: %v", err)
	}
	messages, err = db.ListMessages(ctx, chatID, 10, "")
	if err != nil {
		t.Fatalf("list messages after avatar update: %v", err)
	}
	if messages[0].SenderAvatarLocalPath != "/tmp/alice.jpg" {
		t.Fatalf("sender avatar = %q, want /tmp/alice.jpg", messages[0].SenderAvatarLocalPath)
	}
}

func TestListMessagesReflectsUpdatedSenderPushName(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "1234567890@g.us"
	senderID := "917060029183@s.whatsapp.net"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        chatID + ":msg-1",
		ChatID:    chatID,
		SenderID:  senderID,
		Text:      "hello",
		Timestamp: time.Unix(100, 0),
		Direction: DirectionIncoming,
		Status:    StatusDelivered,
		IsGroup:   true,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
	if err := db.UpdateSenderName(ctx, senderID, "~Alice"); err != nil {
		t.Fatalf("update sender name: %v", err)
	}

	messages, err := db.ListMessages(ctx, chatID, 10, "")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].SenderName != "~Alice" {
		t.Fatalf("sender name = %q, want ~Alice", messages[0].SenderName)
	}
}

func TestListSenderProfilesByChatIDOrdersRecentGroupSenders(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "1234567890@g.us"
	oldSenderID := "111@s.whatsapp.net"
	recentSenderID := "222@s.whatsapp.net"
	for _, input := range []TextMessageInput{
		{
			ID:        chatID + ":old",
			ChatID:    chatID,
			SenderID:  oldSenderID,
			Text:      "old",
			Timestamp: time.Unix(100, 0),
			Direction: DirectionIncoming,
			Status:    StatusDelivered,
			IsGroup:   true,
		},
		{
			ID:        chatID + ":recent",
			ChatID:    chatID,
			SenderID:  recentSenderID,
			Text:      "recent",
			Timestamp: time.Unix(200, 0),
			Direction: DirectionIncoming,
			Status:    StatusDelivered,
			IsGroup:   true,
		},
	} {
		if _, err := db.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save message: %v", err)
		}
	}

	senders, err := db.ListSenderProfilesByChatID(ctx, chatID, 10)
	if err != nil {
		t.Fatalf("list sender profiles: %v", err)
	}
	if len(senders) != 2 {
		t.Fatalf("expected 2 senders, got %d", len(senders))
	}
	if senders[0].ID != recentSenderID || senders[1].ID != oldSenderID {
		t.Fatalf("sender order = [%s %s], want [%s %s]", senders[0].ID, senders[1].ID, recentSenderID, oldSenderID)
	}
}

func TestSaveTextMessageUpdatesChatNameAfterNumericFallback(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "917060029183@s.whatsapp.net"
	first, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":msg-1",
		ChatID:      chatID,
		SenderID:    chatID,
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save fallback message: %v", err)
	}
	if first.Chat.Name != chatID {
		t.Fatalf("expected fallback chat name %q, got %+v", chatID, first.Chat)
	}

	second, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":msg-2",
		ChatID:      chatID,
		ChatName:    "Alice",
		SenderID:    chatID,
		Text:        "history",
		Timestamp:   time.Unix(90, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save named message: %v", err)
	}
	if second.Chat.Name != "Alice" {
		t.Fatalf("expected chat name to update, got %+v", second.Chat)
	}
	if second.Chat.LastMessage != "hello" || second.Chat.LastMessageTime != 100 {
		t.Fatalf("older named message regressed chat summary: %+v", second.Chat)
	}
}

func TestUpdateChatNameUpdatesExistingChat(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "917060029183@s.whatsapp.net"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":msg-1",
		ChatID:      chatID,
		SenderID:    chatID,
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}

	chat, changed, err := db.UpdateChatName(ctx, chatID, "Alice")
	if err != nil {
		t.Fatalf("update chat name: %v", err)
	}
	if !changed || chat.Name != "Alice" {
		t.Fatalf("expected changed chat name, got changed=%v chat=%+v", changed, chat)
	}

	_, changed, err = db.UpdateChatName(ctx, chatID, "Alice")
	if err != nil {
		t.Fatalf("update same chat name: %v", err)
	}
	if changed {
		t.Fatal("expected same chat name update to be ignored")
	}
}

func TestLowerPriorityChatNameDoesNotOverwriteContactName(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "917060029183@s.whatsapp.net"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:             chatID + ":msg-1",
		ChatID:         chatID,
		ChatName:       "Saved Alice",
		ChatNameSource: ChatNameSourceContact,
		SenderID:       chatID,
		Text:           "hello",
		Timestamp:      time.Unix(100, 0),
		Direction:      DirectionIncoming,
		Status:         StatusDelivered,
	}); err != nil {
		t.Fatalf("save contact message: %v", err)
	}

	saved, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:             chatID + ":msg-2",
		ChatID:         chatID,
		ChatName:       "+91 70600 29183",
		ChatNameSource: ChatNameSourcePhone,
		SenderID:       chatID,
		Text:           "newer",
		Timestamp:      time.Unix(200, 0),
		Direction:      DirectionIncoming,
		Status:         StatusDelivered,
	})
	if err != nil {
		t.Fatalf("save phone fallback message: %v", err)
	}
	if saved.Chat.Name != "Saved Alice" || saved.Chat.NameSource != ChatNameSourceContact {
		t.Fatalf("lower priority name overwrote contact: %+v", saved.Chat)
	}
}

func TestPhoneFallbackReplacesRawChatName(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "917060029183@s.whatsapp.net"
	first, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:        chatID + ":msg-1",
		ChatID:    chatID,
		SenderID:  chatID,
		Text:      "hello",
		Timestamp: time.Unix(100, 0),
		Direction: DirectionIncoming,
		Status:    StatusDelivered,
	})
	if err != nil {
		t.Fatalf("save raw fallback message: %v", err)
	}
	if first.Chat.NameSource != ChatNameSourceRaw {
		t.Fatalf("expected raw initial source, got %+v", first.Chat)
	}

	second, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:             chatID + ":msg-2",
		ChatID:         chatID,
		ChatName:       "+91 70600 29183",
		ChatNameSource: ChatNameSourcePhone,
		SenderID:       chatID,
		Text:           "newer",
		Timestamp:      time.Unix(200, 0),
		Direction:      DirectionIncoming,
		Status:         StatusDelivered,
	})
	if err != nil {
		t.Fatalf("save phone fallback message: %v", err)
	}
	if second.Chat.Name != "+91 70600 29183" || second.Chat.NameSource != ChatNameSourcePhone {
		t.Fatalf("phone fallback did not replace raw name: %+v", second.Chat)
	}
}

func TestListChatsFormatsWhatsAppNameForDirectChat(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "917060029183@s.whatsapp.net"
	if _, err := db.EnsureChatWithNameSource(ctx, chatID, "~Alice", ChatNameSourceWhatsApp, false); err != nil {
		t.Fatalf("ensure chat: %v", err)
	}

	chats, err := db.ListChats(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("got %d chats, want 1", len(chats))
	}
	if chats[0].Name != "+91 70600 29183" || chats[0].NameSource != ChatNameSourcePhone {
		t.Fatalf("listed chat = %+v, want formatted phone", chats[0])
	}

	stored, err := db.GetChat(ctx, chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if stored.Name != "~Alice" || stored.NameSource != ChatNameSourceWhatsApp {
		t.Fatalf("stored chat = %+v, want original WhatsApp name", stored)
	}
}

func TestGroupNameDoesNotRegressToRawLiveName(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "1234567890@g.us"
	if _, err := db.EnsureChatWithNameSource(ctx, chatID, "Family", ChatNameSourceGroup, true); err != nil {
		t.Fatalf("ensure group chat: %v", err)
	}

	saved, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:             chatID + ":msg-1",
		ChatID:         chatID,
		ChatName:       "1234567890",
		ChatNameSource: ChatNameSourceRaw,
		SenderID:       "917060029183@s.whatsapp.net",
		Text:           "hello",
		Timestamp:      time.Unix(100, 0),
		Direction:      DirectionIncoming,
		Status:         StatusDelivered,
		IsGroup:        true,
	})
	if err != nil {
		t.Fatalf("save group message: %v", err)
	}
	if saved.Chat.Name != "Family" || saved.Chat.NameSource != ChatNameSourceGroup {
		t.Fatalf("group name regressed: %+v", saved.Chat)
	}
}

func TestListRawGroupChatIDsReturnsOnlyUnresolvedGroups(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, input := range []TextMessageInput{
		{ID: "120363111@g.us:1", ChatID: "120363111@g.us", SenderID: "sender@s.whatsapp.net", Text: "raw group", Timestamp: time.Unix(300, 0), IsGroup: true},
		{ID: "status@broadcast:1", ChatID: "status@broadcast", SenderID: "sender@s.whatsapp.net", Text: "status", Timestamp: time.Unix(200, 0), IsGroup: true},
		{ID: "person@s.whatsapp.net:1", ChatID: "person@s.whatsapp.net", SenderID: "person@s.whatsapp.net", Text: "dm", Timestamp: time.Unix(100, 0)},
	} {
		if _, err := db.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save message %s: %v", input.ID, err)
		}
	}
	if _, err := db.EnsureChatWithNameSource(ctx, "120363222@g.us", "Named Group", ChatNameSourceGroup, true); err != nil {
		t.Fatalf("ensure named group: %v", err)
	}

	chatIDs, err := db.ListRawGroupChatIDs(ctx, 100)
	if err != nil {
		t.Fatalf("list raw group chats: %v", err)
	}
	if len(chatIDs) != 1 || chatIDs[0] != "120363111@g.us" {
		t.Fatalf("unexpected raw group chats: %#v", chatIDs)
	}
}

func TestListChatsSortsByMostRecentMessage(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-a:1",
		ChatID:      "chat-a",
		ChatName:    "Alpha",
		SenderID:    "sender-a",
		Text:        "older",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save alpha: %v", err)
	}

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-b:1",
		ChatID:      "chat-b",
		ChatName:    "Beta",
		SenderID:    "sender-b",
		Text:        "newer",
		Timestamp:   time.Unix(200, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save beta: %v", err)
	}

	chats, err := db.ListChats(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chats))
	}
	if chats[0].ID != "chat-b" || chats[1].ID != "chat-a" {
		t.Fatalf("unexpected chat order: %+v", chats)
	}
}

func TestListMessagesReturnsAscendingOrderAndPagination(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, item := range []struct {
		id string
		ts int64
	}{
		{id: "chat-1:1", ts: 100},
		{id: "chat-1:2", ts: 200},
		{id: "chat-1:3", ts: 300},
	} {
		_, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:          item.id,
			ChatID:      "chat-1",
			ChatName:    "Test Chat",
			SenderID:    "sender-1",
			Text:        item.id,
			Timestamp:   time.Unix(item.ts, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		})
		if err != nil {
			t.Fatalf("save %s: %v", item.id, err)
		}
	}

	latestTwo, err := db.ListMessages(ctx, "chat-1", 2, "")
	if err != nil {
		t.Fatalf("list latest messages: %v", err)
	}
	if len(latestTwo) != 2 || latestTwo[0].ID != "chat-1:2" || latestTwo[1].ID != "chat-1:3" {
		t.Fatalf("unexpected latest messages: %+v", latestTwo)
	}

	older, err := db.ListMessages(ctx, "chat-1", 2, "chat-1:2")
	if err != nil {
		t.Fatalf("list paginated messages: %v", err)
	}
	if len(older) != 1 || older[0].ID != "chat-1:1" {
		t.Fatalf("unexpected paginated messages: %+v", older)
	}
}

func TestListMessagesAroundReturnsBoundedWindow(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for i := 1; i <= 7; i++ {
		id := fmt.Sprintf("chat-1:%d", i)
		_, err := db.SaveTextMessage(ctx, TextMessageInput{
			ID:          id,
			ChatID:      "chat-1",
			ChatName:    "Test Chat",
			SenderID:    "sender-1",
			Text:        id,
			Timestamp:   time.Unix(int64(i*100), 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		})
		if err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	messages, err := db.ListMessagesAround(ctx, "chat-1", 5, "chat-1:4")
	if err != nil {
		t.Fatalf("list around: %v", err)
	}
	got := make([]string, 0, len(messages))
	for _, message := range messages {
		got = append(got, message.ID)
	}
	want := []string{"chat-1:2", "chat-1:3", "chat-1:4", "chat-1:5", "chat-1:6"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("around ids = %+v, want %+v", got, want)
	}
}

func TestListMessagesAroundMissingOrWrongChatReturnsNoRows(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := db.ListMessagesAround(ctx, "chat-1", 5, "missing"); err != sql.ErrNoRows {
		t.Fatalf("missing target error = %v, want sql.ErrNoRows", err)
	}
	if _, err := db.ListMessagesAround(ctx, "other-chat", 5, "chat-1:1"); err != sql.ErrNoRows {
		t.Fatalf("wrong chat error = %v, want sql.ErrNoRows", err)
	}
}

func TestMarkChatReadClearsUnreadCount(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}

	chat, err := db.MarkChatRead(ctx, "chat-1")
	if err != nil {
		t.Fatalf("mark chat read: %v", err)
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("expected unread_count 0, got %+v", chat)
	}
}

func TestClearSessionDataDeletesChatsMessagesAndAppState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "sender-1",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
	if _, err := db.conn.ExecContext(ctx, `INSERT INTO app_state (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatalf("insert app state: %v", err)
	}
	if _, err := db.SaveHistorySyncChunk(ctx, HistorySyncChunk{ID: "hist-1", SyncType: 3}); err != nil {
		t.Fatalf("insert history sync chunk: %v", err)
	}

	if err := db.ClearSessionData(ctx); err != nil {
		t.Fatalf("clear session data: %v", err)
	}

	chats, err := db.ListChats(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 0 {
		t.Fatalf("expected no chats after clear, got %+v", chats)
	}
	if _, err := db.GetMessage(ctx, "chat-1:1"); err != sql.ErrNoRows {
		t.Fatalf("expected message to be deleted, got %v", err)
	}

	var stateCount int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_state`).Scan(&stateCount); err != nil {
		t.Fatalf("count app state: %v", err)
	}
	if stateCount != 0 {
		t.Fatalf("expected app_state to be empty, got %d rows", stateCount)
	}
	var historySyncCount int
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM history_sync_chunks`).Scan(&historySyncCount); err != nil {
		t.Fatalf("count history sync chunks: %v", err)
	}
	if historySyncCount != 0 {
		t.Fatalf("expected history_sync_chunks to be empty, got %d rows", historySyncCount)
	}
}

func TestHistorySyncChunksAreRecoverableUntilAcked(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	inserted, err := db.SaveHistorySyncChunk(ctx, HistorySyncChunk{
		ID:            "hist-1",
		SyncType:      3,
		ChunkOrder:    2,
		Progress:      50,
		FileLength:    253336,
		DirectPath:    "/history",
		MediaKey:      []byte{1, 2},
		FileSHA256:    []byte{3, 4},
		FileEncSHA256: []byte{5, 6},
		EncHandle:     "enc",
	})
	if err != nil {
		t.Fatalf("save chunk: %v", err)
	}
	if !inserted {
		t.Fatal("expected first chunk save to insert")
	}

	inserted, err = db.SaveHistorySyncChunk(ctx, HistorySyncChunk{ID: "hist-1", SyncType: 3})
	if err != nil {
		t.Fatalf("save duplicate chunk: %v", err)
	}
	if inserted {
		t.Fatal("expected duplicate chunk save to be ignored")
	}

	if err := db.MarkHistorySyncChunkProcessing(ctx, "hist-1"); err != nil {
		t.Fatalf("mark processing: %v", err)
	}
	chunks, err := db.ListRecoverableHistorySyncChunks(ctx, 10)
	if err != nil {
		t.Fatalf("list recoverable chunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].ID != "hist-1" || chunks[0].Status != HistorySyncStatusProcessing || chunks[0].Attempts != 1 || chunks[0].FileLength != 253336 {
		t.Fatalf("unexpected recoverable chunks: %+v", chunks)
	}

	if err := db.MarkHistorySyncChunkProcessed(ctx, "hist-1"); err != nil {
		t.Fatalf("mark processed: %v", err)
	}
	chunks, err = db.ListRecoverableHistorySyncChunks(ctx, 10)
	if err != nil {
		t.Fatalf("list processed recoverable chunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].Status != HistorySyncStatusProcessed {
		t.Fatalf("processed chunk should remain recoverable for ack retry: %+v", chunks)
	}

	if err := db.MarkHistorySyncChunkAcked(ctx, "hist-1"); err != nil {
		t.Fatalf("mark acked: %v", err)
	}
	chunks, err = db.ListRecoverableHistorySyncChunks(ctx, 10)
	if err != nil {
		t.Fatalf("list acked recoverable chunks: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("acked chunk should not be recoverable: %+v", chunks)
	}
}

func TestListRecoverableHistorySyncChunksOrdersByTypeAndChunk(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, chunk := range []HistorySyncChunk{
		{ID: "recent-12", SyncType: 3, ChunkOrder: 12},
		{ID: "recent-11", SyncType: 3, ChunkOrder: 11},
		{ID: "status-0", SyncType: 1, ChunkOrder: 0},
	} {
		if _, err := db.SaveHistorySyncChunk(ctx, chunk); err != nil {
			t.Fatalf("save chunk %s: %v", chunk.ID, err)
		}
	}

	chunks, err := db.ListRecoverableHistorySyncChunks(ctx, 10)
	if err != nil {
		t.Fatalf("list recoverable chunks: %v", err)
	}
	got := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		got = append(got, chunk.ID)
	}
	want := []string{"status-0", "recent-11", "recent-12"}
	if len(got) != len(want) {
		t.Fatalf("got chunks %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got chunks %v, want %v", got, want)
		}
	}
}

func TestReadCandidatesAndMarkMessagesRead(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:incoming-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "123@s.whatsapp.net",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	})
	if err != nil {
		t.Fatalf("save incoming unread: %v", err)
	}

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:incoming-history",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "123@s.whatsapp.net",
		Text:        "history",
		Timestamp:   time.Unix(90, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save incoming history: %v", err)
	}

	candidates, err := db.ReadCandidatesForChat(ctx, "chat-1")
	if err != nil {
		t.Fatalf("read candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ExternalID != "incoming-1" {
		t.Fatalf("unexpected read candidates: %+v", candidates)
	}

	chat, err := db.MarkMessagesRead(ctx, "chat-1")
	if err != nil {
		t.Fatalf("mark messages read: %v", err)
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("expected unread count 0 after mark read, got %+v", chat)
	}

	message, err := db.GetMessage(ctx, "chat-1:incoming-1")
	if err != nil {
		t.Fatalf("get message after mark read: %v", err)
	}
	if !message.IsRead {
		t.Fatalf("expected message to be read after mark read: %+v", message)
	}
}

func TestUpdateMessageStatusProgression(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:sent-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "me",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionOutgoing,
		Status:      StatusSent,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save outgoing message: %v", err)
	}

	message, changed, err := db.UpdateMessageStatus(ctx, "chat-1:sent-1", StatusDelivered)
	if err != nil {
		t.Fatalf("set delivered: %v", err)
	}
	if !changed || message.Status != StatusDelivered {
		t.Fatalf("unexpected delivered update: %+v changed=%v", message, changed)
	}

	message, changed, err = db.UpdateMessageStatus(ctx, "chat-1:sent-1", StatusRead)
	if err != nil {
		t.Fatalf("set read: %v", err)
	}
	if !changed || message.Status != StatusRead {
		t.Fatalf("unexpected read update: %+v changed=%v", message, changed)
	}

	message, changed, err = db.UpdateMessageStatus(ctx, "chat-1:sent-1", StatusDelivered)
	if err != nil {
		t.Fatalf("attempt downgrade: %v", err)
	}
	if changed || message.Status != StatusRead {
		t.Fatalf("unexpected downgrade result: %+v changed=%v", message, changed)
	}
}

func TestUpdateMessageStatusFromHistoryCorrectsFalseDelivered(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:sent-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "me",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionOutgoing,
		Status:      StatusDelivered,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save outgoing message: %v", err)
	}

	message, changed, err := db.UpdateMessageStatusFromHistory(ctx, "chat-1:sent-1", StatusSent)
	if err != nil {
		t.Fatalf("correct from history: %v", err)
	}
	if !changed || message.Status != StatusSent {
		t.Fatalf("unexpected history correction: %+v changed=%v", message, changed)
	}

	chat, err := db.GetChat(ctx, "chat-1")
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.LastMessageStatus != StatusSent {
		t.Fatalf("chat last status = %q, want %q", chat.LastMessageStatus, StatusSent)
	}
}

func TestUpdateMessageStatusFromHistoryDoesNotDowngradeRead(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.SaveTextMessage(ctx, TextMessageInput{
		ID:          "chat-1:read-1",
		ChatID:      "chat-1",
		ChatName:    "Test Chat",
		SenderID:    "me",
		Text:        "hello",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionOutgoing,
		Status:      StatusRead,
		CountUnread: false,
	})
	if err != nil {
		t.Fatalf("save outgoing message: %v", err)
	}

	message, changed, err := db.UpdateMessageStatusFromHistory(ctx, "chat-1:read-1", StatusSent)
	if err != nil {
		t.Fatalf("history update: %v", err)
	}
	if changed || message.Status != StatusRead {
		t.Fatalf("unexpected read downgrade: %+v changed=%v", message, changed)
	}
}

func TestListPendingOutgoingMessages(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	inputs := []TextMessageInput{
		{
			ID:          "chat-1:pending-2",
			ChatID:      "chat-1",
			ChatName:    "Test Chat",
			SenderID:    "me",
			Text:        "second",
			Timestamp:   time.Unix(200, 0),
			Direction:   DirectionOutgoing,
			Status:      StatusPending,
			CountUnread: false,
		},
		{
			ID:          "chat-1:sent-1",
			ChatID:      "chat-1",
			ChatName:    "Test Chat",
			SenderID:    "me",
			Text:        "already sent",
			Timestamp:   time.Unix(150, 0),
			Direction:   DirectionOutgoing,
			Status:      StatusSent,
			CountUnread: false,
		},
		{
			ID:          "chat-1:pending-1",
			ChatID:      "chat-1",
			ChatName:    "Test Chat",
			SenderID:    "me",
			Text:        "first",
			Timestamp:   time.Unix(100, 0),
			Direction:   DirectionOutgoing,
			Status:      StatusPending,
			CountUnread: false,
		},
		{
			ID:          "chat-1:incoming-pending",
			ChatID:      "chat-1",
			ChatName:    "Test Chat",
			SenderID:    "sender-1",
			Text:        "incoming",
			Timestamp:   time.Unix(50, 0),
			Direction:   DirectionIncoming,
			Status:      StatusPending,
			CountUnread: true,
		},
	}

	for _, input := range inputs {
		if _, err := db.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save %s: %v", input.ID, err)
		}
	}

	messages, err := db.ListPendingOutgoingMessages(ctx, 10, time.Now())
	if err != nil {
		t.Fatalf("list pending outgoing: %v", err)
	}

	got := make([]string, 0, len(messages))
	for _, message := range messages {
		got = append(got, message.ID)
	}
	want := []string{"chat-1:pending-1", "chat-1:pending-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pending outgoing order: got %v want %v", got, want)
	}
}

func TestListLIDChats(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, input := range []TextMessageInput{
		{
			ID:          "30413133201647@lid:lid-1",
			ChatID:      "30413133201647@lid",
			ChatName:    "LID Chat",
			SenderID:    "30413133201647@lid",
			Text:        "lid",
			Timestamp:   time.Unix(100, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		},
		{
			ID:          "917060029183@s.whatsapp.net:pn-1",
			ChatID:      "917060029183@s.whatsapp.net",
			ChatName:    "PN Chat",
			SenderID:    "917060029183@s.whatsapp.net",
			Text:        "pn",
			Timestamp:   time.Unix(200, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		},
		{
			ID:          "12345-67890@g.us:group-1",
			ChatID:      "12345-67890@g.us",
			ChatName:    "Group",
			SenderID:    "917060029183@s.whatsapp.net",
			Text:        "group",
			Timestamp:   time.Unix(300, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
			IsGroup:     true,
		},
	} {
		if _, err := db.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save %s: %v", input.ID, err)
		}
	}

	lidChats, err := db.ListLIDChats(ctx)
	if err != nil {
		t.Fatalf("list lid chats: %v", err)
	}

	want := []string{"30413133201647@lid"}
	if !reflect.DeepEqual(lidChats, want) {
		t.Fatalf("unexpected lid chats: got %v want %v", lidChats, want)
	}
}

func TestMigrateChatIDReturnsFalseWhenSourceMissing(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chat, migrated, err := db.MigrateChatID(ctx, "30413133201647@lid", "917060029183@s.whatsapp.net")
	if err != nil {
		t.Fatalf("migrate missing chat: %v", err)
	}
	if migrated {
		t.Fatal("expected migration to be skipped")
	}
	if chat != (Chat{}) {
		t.Fatalf("expected zero chat on skipped migration, got %+v", chat)
	}
}

func TestMigrateChatIDMovesMessagesToTarget(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	fromChatID := "30413133201647@lid"
	toChatID := "917060029183@s.whatsapp.net"

	for _, input := range []TextMessageInput{
		{
			ID:          fromChatID + ":msg-1",
			ChatID:      fromChatID,
			ChatName:    "Alice",
			SenderID:    fromChatID,
			Text:        "hello",
			Timestamp:   time.Unix(100, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: true,
		},
		{
			ID:          fromChatID + ":msg-2",
			ChatID:      fromChatID,
			ChatName:    "Alice",
			SenderID:    fromChatID,
			Text:        "history",
			Timestamp:   time.Unix(90, 0),
			Direction:   DirectionIncoming,
			Status:      StatusDelivered,
			CountUnread: false,
		},
	} {
		if _, err := db.SaveTextMessage(ctx, input); err != nil {
			t.Fatalf("save %s: %v", input.ID, err)
		}
	}

	chat, migrated, err := db.MigrateChatID(ctx, fromChatID, toChatID)
	if err != nil {
		t.Fatalf("migrate chat: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to happen")
	}
	if chat.ID != toChatID || chat.Name != "Alice" || chat.LastMessage != "hello" || chat.LastMessageTime != 100 || chat.UnreadCount != 1 {
		t.Fatalf("unexpected migrated chat: %+v", chat)
	}

	if _, err := db.GetChat(ctx, fromChatID); err != sql.ErrNoRows {
		t.Fatalf("expected source chat to be deleted, got %v", err)
	}

	messages, err := db.ListMessages(ctx, toChatID, 10, "")
	if err != nil {
		t.Fatalf("list migrated messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 migrated messages, got %d", len(messages))
	}
	if messages[0].ID != toChatID+":msg-2" || messages[0].ChatID != toChatID {
		t.Fatalf("unexpected first migrated message: %+v", messages[0])
	}
	if messages[1].ID != toChatID+":msg-1" || messages[1].ChatID != toChatID {
		t.Fatalf("unexpected second migrated message: %+v", messages[1])
	}

	if _, err := db.GetMessage(ctx, fromChatID+":msg-1"); err != sql.ErrNoRows {
		t.Fatalf("expected source message to be deleted, got %v", err)
	}
	if _, err := db.GetMessage(ctx, toChatID+":msg-1"); err != nil {
		t.Fatalf("expected rewritten message to exist: %v", err)
	}
}

func TestMigrateChatIDMergesExistingTargetWithoutDoubleCountingUnread(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	fromChatID := "30413133201647@lid"
	toChatID := "917060029183@s.whatsapp.net"

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          fromChatID + ":dup-1",
		ChatID:      fromChatID,
		ChatName:    "Alice",
		SenderID:    fromChatID,
		Text:        "duplicate",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save lid message: %v", err)
	}

	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          toChatID + ":dup-1",
		ChatID:      toChatID,
		ChatName:    "",
		SenderID:    toChatID,
		Text:        "newer target",
		Timestamp:   time.Unix(200, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save target message: %v", err)
	}

	chat, migrated, err := db.MigrateChatID(ctx, fromChatID, toChatID)
	if err != nil {
		t.Fatalf("migrate chat: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to happen")
	}
	if chat.Name != "Alice" {
		t.Fatalf("expected target name to be replaced from default id, got %+v", chat)
	}
	if chat.LastMessage != "newer target" || chat.LastMessageTime != 200 {
		t.Fatalf("expected newer target summary to win, got %+v", chat)
	}
	if chat.UnreadCount != 1 {
		t.Fatalf("expected unread count to be recomputed to 1, got %+v", chat)
	}

	messages, err := db.ListMessages(ctx, toChatID, 10, "")
	if err != nil {
		t.Fatalf("list merged messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected duplicate message to be deduped, got %d", len(messages))
	}
}

func TestOverwriteChatUnreadCountSetsBadgeAndMarksReadWhenZero(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "chat-overwrite"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":a",
		ChatID:      chatID,
		ChatName:    "Test",
		SenderID:    "sender-1",
		Text:        "first",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":b",
		ChatID:      chatID,
		ChatName:    "Test",
		SenderID:    "sender-1",
		Text:        "second",
		Timestamp:   time.Unix(200, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: true,
	}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	// Phone says the conversation is read; overwrite to 0.
	chat, changed, err := db.OverwriteChatUnreadCount(ctx, chatID, 0)
	if err != nil {
		t.Fatalf("overwrite unread to zero: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true for non-zero -> zero transition")
	}
	if chat.UnreadCount != 0 {
		t.Fatalf("expected unread_count=0, got %+v", chat)
	}

	// Both messages should now be marked read so badge stays at 0 even
	// after a future MarkChatRead recompute.
	candidates, err := db.ReadCandidatesForChat(ctx, chatID)
	if err != nil {
		t.Fatalf("read candidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no remaining unread candidates, got %d", len(candidates))
	}
}

func TestEnsureChatAllowsUnreadOverwriteWithoutMessages(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chat, err := db.EnsureChat(ctx, "chat-empty", "Empty Chat", true)
	if err != nil {
		t.Fatalf("ensure chat: %v", err)
	}
	if chat.Name != "Empty Chat" || !chat.IsGroup {
		t.Fatalf("unexpected ensured chat: %+v", chat)
	}

	chat, changed, err := db.OverwriteChatUnreadCount(ctx, "chat-empty", 3)
	if err != nil {
		t.Fatalf("overwrite unread: %v", err)
	}
	if !changed || chat.UnreadCount != 3 {
		t.Fatalf("unexpected overwrite result: changed=%v chat=%+v", changed, chat)
	}
}

func TestOverwriteChatUnreadCountReportsUnchangedWhenSame(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "chat-noop"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":a",
		ChatID:      chatID,
		ChatName:    "Test",
		SenderID:    "sender-1",
		Text:        "hi",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, changed, err := db.OverwriteChatUnreadCount(ctx, chatID, 0)
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false when value already matches")
	}
}

func TestOverwriteChatUnreadCountSetsNonZeroBadge(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "whatevrd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	chatID := "chat-nonzero"
	if _, err := db.SaveTextMessage(ctx, TextMessageInput{
		ID:          chatID + ":a",
		ChatID:      chatID,
		ChatName:    "Test",
		SenderID:    "sender-1",
		Text:        "hi",
		Timestamp:   time.Unix(100, 0),
		Direction:   DirectionIncoming,
		Status:      StatusDelivered,
		CountUnread: false,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	chat, changed, err := db.OverwriteChatUnreadCount(ctx, chatID, 7)
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when overwriting to a new value")
	}
	if chat.UnreadCount != 7 {
		t.Fatalf("expected unread_count=7, got %+v", chat)
	}

	// With unread=7 we did NOT mark messages read. ReadCandidates is
	// based on per-message is_read which we shouldn't have touched.
	if _, err := db.GetMessage(ctx, chatID+":a"); err == sql.ErrNoRows {
		t.Fatalf("message disappeared after overwrite")
	}
}
