package wa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func (c *Client) handleHistorySync(evt *events.HistorySync) {
	client := c.currentClient()
	if client == nil {
		return
	}
	for _, conv := range evt.Data.GetConversations() {
		chatJID, err := types.ParseJID(conv.GetID())
		if err != nil {
			c.log.Warnf("Failed to parse chat JID in history sync: %v", err)
			continue
		}
		for _, msg := range conv.GetMessages() {
			webMsg := msg.GetMessage()
			if webMsg == nil {
				continue
			}
			parsedEvt, err := client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				c.log.Warnf("Failed to parse history sync message: %v", err)
				continue
			}
			c.handleMessage(context.Background(), parsedEvt)
		}
	}
}

func (c *Client) handleMessage(ctx context.Context, evt *events.Message) {
	if textInput, ok := c.textMessageInput(ctx, evt); ok {
		saved, err := c.store.SaveTextMessage(ctx, textInput)
		if err != nil {
			c.log.Errorf("Failed to store text message %s: %v", textInput.ID, err)
			return
		}
		if !saved.Inserted {
			return
		}
		c.log.Infof("Stored text message %s from %s", saved.Message.ID, saved.Message.SenderID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
		return
	}

	if mediaInput, ok := c.imageMessageInput(ctx, evt); ok {
		saved, err := c.store.SaveMediaMessage(ctx, mediaInput)
		if err != nil {
			c.log.Errorf("Failed to store media message %s: %v", mediaInput.ID, err)
			return
		}
		if !saved.Inserted {
			return
		}
		c.log.Infof("Stored media message %s from %s", saved.Message.ID, saved.Message.SenderID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
}

func (c *Client) imageMessageInput(ctx context.Context, evt *events.Message) (appstore.MediaMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.MediaMessageInput{}, false
	}

	imgMsg := evt.Message.GetImageMessage()
	if imgMsg == nil {
		return appstore.MediaMessageInput{}, false
	}

	info := evt.Info
	chatJID := c.normalizeJIDForChat(ctx, info.Chat)
	chatID := chatJID.String()
	if chatID == "" || info.ID == "" {
		return appstore.MediaMessageInput{}, false
	}

	direction := appstore.DirectionIncoming
	status := appstore.StatusDelivered
	if info.IsFromMe {
		direction = appstore.DirectionOutgoing
		status = appstore.StatusSent
	}

	client := c.currentClient()
	if client == nil {
		return appstore.MediaMessageInput{}, false
	}

	data, err := client.Download(ctx, imgMsg)
	if err != nil {
		c.log.Warnf("Failed to download image for message %s: %v", info.ID, err)
		return appstore.MediaMessageInput{}, false
	}

	mediaDir := filepath.Join(c.paths.MediaCacheDir, "messages", chatID)
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		c.log.Warnf("Failed to create media dir for %s: %v", chatID, err)
		return appstore.MediaMessageInput{}, false
	}

	localPath := filepath.Join(mediaDir, fmt.Sprintf("%s.jpg", info.ID))
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		c.log.Warnf("Failed to write image for message %s: %v", info.ID, err)
		return appstore.MediaMessageInput{}, false
	}

	caption := imgMsg.GetCaption()
	return appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, info.ID),
			ChatID:      chatID,
			ChatName:    chatName(info),
			SenderID:    senderID(info),
			Text:        caption,
			Timestamp:   info.Timestamp,
			Direction:   direction,
			Status:      status,
			IsGroup:     info.IsGroup,
			CountUnread: !info.IsFromMe && evt.SourceWebMsg == nil,
		},
		MediaMimeType:  "image/jpeg",
		MediaLocalPath: localPath,
		MediaWidth:     int32(imgMsg.GetWidth()),
		MediaHeight:    int32(imgMsg.GetHeight()),
	}, true
}

func (c *Client) normalizeJIDForChat(ctx context.Context, jid types.JID) types.JID {
	if jid.Server != types.HiddenUserServer {
		return jid
	}

	client := c.currentClient()
	if client == nil || client.Store.LIDs == nil {
		return jid
	}

	pn, err := client.Store.LIDs.GetPNForLID(ctx, jid)
	if err != nil || pn.IsEmpty() {
		return jid
	}

	return pn
}

func (c *Client) textMessageInput(ctx context.Context, evt *events.Message) (appstore.TextMessageInput, bool) {
	if evt == nil || evt.Message == nil {
		return appstore.TextMessageInput{}, false
	}

	text := textFromMessage(evt.Message)
	if strings.TrimSpace(text) == "" {
		return appstore.TextMessageInput{}, false
	}

	info := evt.Info
	chatJID := c.normalizeJIDForChat(ctx, info.Chat)
	chatID := chatJID.String()
	if chatID == "" {
		return appstore.TextMessageInput{}, false
	}
	if info.ID == "" {
		return appstore.TextMessageInput{}, false
	}

	direction := appstore.DirectionIncoming
	status := appstore.StatusDelivered
	if info.IsFromMe {
		direction = appstore.DirectionOutgoing
		status = appstore.StatusSent
	}

	return appstore.TextMessageInput{
		ID:          internalMessageIDForChat(chatID, info.ID),
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
		ID:             message.ID,
		ChatID:         message.ChatID,
		SenderID:       message.SenderID,
		Text:           message.Text,
		TimestampUnix:  message.TimestampUnix,
		Direction:      message.Direction,
		Status:         message.Status,
		MediaMimeType:  message.MediaMimeType,
		MediaLocalPath: message.MediaLocalPath,
		MediaWidth:     message.MediaWidth,
		MediaHeight:    message.MediaHeight,
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
		AvatarLocalPath: chat.AvatarLocalPath,
	}
}
