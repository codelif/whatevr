package wa

import (
	"context"
	"strings"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func (c *Client) handleMessage(evt *events.Message) {
	input, ok := c.textMessageInput(evt)
	if !ok {
		return
	}

	saved, err := c.store.SaveTextMessage(context.Background(), input)
	if err != nil {
		c.log.Errorf("Failed to store message %s: %v", input.ID, err)
		return
	}
	if !saved.Inserted {
		return
	}

	c.log.Infof("Stored text message %s from %s", saved.Message.ID, saved.Message.SenderID)
	c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
}

func (c *Client) textMessageInput(evt *events.Message) (appstore.TextMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.TextMessageInput{}, false
	}

	text := textFromMessage(evt.Message)
	if strings.TrimSpace(text) == "" {
		return appstore.TextMessageInput{}, false
	}

	info := evt.Info
	chatID := info.Chat.String()
	if chatID == "" {
		return appstore.TextMessageInput{}, false
	}
	if info.ID == "" {
		return appstore.TextMessageInput{}, false
	}

	direction := appstore.DirectionIncoming
	status := appstore.StatusReceived
	if info.IsFromMe {
		direction = appstore.DirectionOutgoing
		status = appstore.StatusSent
	}

	return appstore.TextMessageInput{
		ID:          internalMessageID(info),
		ChatID:      chatID,
		ChatName:    chatName(info),
		SenderID:    senderID(info),
		Text:        text,
		Timestamp:   info.Timestamp,
		Direction:   direction,
		Status:      status,
		IsGroup:     info.IsGroup,
		CountUnread: !info.IsFromMe && evt.SourceWebMsg == nil,
	}, true
}

func textFromMessage(message *waE2E.Message) string {
	if text := message.GetConversation(); text != "" {
		return text
	}
	if text := message.GetExtendedTextMessage().GetText(); text != "" {
		return text
	}
	return ""
}

func internalMessageID(info types.MessageInfo) string {
	return internalMessageIDForChat(info.Chat.String(), info.ID)
}

func senderID(info types.MessageInfo) string {
	if info.IsFromMe {
		return "me"
	}
	if !info.Sender.IsEmpty() {
		return info.Sender.String()
	}
	return info.Chat.String()
}

func chatName(info types.MessageInfo) string {
	if info.IsFromMe {
		return ""
	}
	if !info.IsGroup && info.PushName != "" {
		return info.PushName
	}
	if info.Chat.User != "" {
		return info.Chat.User
	}
	return info.Chat.String()
}

func toDaemonMessage(message appstore.Message) app.Message {
	return app.Message{
		ID:            message.ID,
		ChatID:        message.ChatID,
		SenderID:      message.SenderID,
		Text:          message.Text,
		TimestampUnix: message.TimestampUnix,
		Direction:     message.Direction,
		Status:        message.Status,
	}
}

func toDaemonChat(chat appstore.Chat) app.Chat {
	return app.Chat{
		ID:              chat.ID,
		Name:            chat.Name,
		LastMessage:     chat.LastMessage,
		LastMessageTime: chat.LastMessageTime,
		UnreadCount:     chat.UnreadCount,
		IsGroup:         chat.IsGroup,
	}
}
