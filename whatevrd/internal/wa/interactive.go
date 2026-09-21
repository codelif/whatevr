package wa

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// This file implements the interactive message kinds: polls (create),
// contact cards and locations. Polls send immediately (like reactions)
// rather than through the media queue: they carry no bytes.

// SendPoll creates a single- or multi-select poll. Options are trimmed,
// deduplicated, and capped the way official clients cap them (2–12).
func (c *Client) SendPoll(ctx context.Context, chatID, question string, options []string, multi bool) (appstore.SavedTextMessage, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "poll question is required")
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" || seen[option] {
			continue
		}
		seen[option] = true
		clean = append(clean, option)
	}
	if len(clean) < 2 {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "a poll needs at least two options")
	}
	if len(clean) > 12 {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "a poll holds at most 12 options")
	}
	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()

	selectable := 1
	if multi {
		selectable = len(clean)
	}
	built := client.BuildPollCreation(question, clean, selectable)
	messageID := client.GenerateMessageID()
	if _, err := client.SendMessage(ctx, targetJID, built, whatsmeow.SendRequestExtra{ID: messageID}); err != nil {
		return appstore.SavedTextMessage{}, err
	}
	payload, err := appstore.EncodePayload(appstore.MessagePayload{Poll: &appstore.PollPayload{
		Question:        question,
		SelectableCount: selectable,
	}})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	hashes := whatsmeow.HashPollOptions(clean)
	saved, err := c.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, messageID),
			ChatID:      chatID,
			SenderID:    "me",
			Text:        question,
			Timestamp:   time.Now(),
			Direction:   appstore.DirectionOutgoing,
			Status:      appstore.StatusSent,
			IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
			CountUnread: false,
			PayloadJSON: payload,
		},
		MediaKind:      appstore.MediaKindPoll,
		PayloadSummary: question,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	pollOptions := make([]appstore.PollOption, 0, len(clean))
	for i, name := range clean {
		pollOptions = append(pollOptions, appstore.PollOption{Index: i, Name: name, SHA256: hashes[i]})
	}
	if err := c.store.SavePollOptions(ctx, saved.Message.ID, pollOptions); err != nil {
		c.log.Warnf("Failed to store poll options for %s: %v", saved.Message.ID, err)
	}
	if saved.Inserted {
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	return saved, nil
}

// SendContact shares a contact card (name + phone) as a vCard message.
func (c *Client) SendContact(ctx context.Context, chatID, name, phone string) (appstore.SavedTextMessage, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	name = strings.TrimSpace(name)
	phone = strings.TrimSpace(phone)
	if name == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "contact name is required")
	}
	if phone == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "contact phone is required")
	}
	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()

	vcard := fmt.Sprintf("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:%s\r\nTEL;TYPE=CELL:%s\r\nEND:VCARD", vcardEscape(name), vcardEscape(phone))
	contactMsg := &waE2E.ContactMessage{
		DisplayName: proto.String(name),
		Vcard:       proto.String(vcard),
	}
	messageID := client.GenerateMessageID()
	if _, err := client.SendMessage(ctx, targetJID, &waE2E.Message{ContactMessage: contactMsg}, whatsmeow.SendRequestExtra{ID: messageID}); err != nil {
		return appstore.SavedTextMessage{}, err
	}
	payload, _ := proto.Marshal(contactMsg)
	card := appstore.MessagePayload{Contacts: &appstore.ContactsPayload{Cards: []appstore.ContactCard{{
		DisplayName: name,
		Phones:      []appstore.ContactField{{Label: "CELL", Value: phone}},
		VCard:       vcard,
	}}}}
	encoded, err := appstore.EncodePayload(card)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	saved, err := c.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, messageID),
			ChatID:      chatID,
			SenderID:    "me",
			Text:        name,
			Timestamp:   time.Now(),
			Direction:   appstore.DirectionOutgoing,
			Status:      appstore.StatusSent,
			IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
			CountUnread: false,
			PayloadJSON: encoded,
		},
		MediaKind:      appstore.MediaKindContact,
		MediaMimeType:  "text/vcard",
		MediaPayload:   payload,
		PayloadSummary: name,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	if saved.Inserted {
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	return saved, nil
}

func vcardEscape(s string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\n", "\\n", ",", "\\,", ";", "\\;")
	return replacer.Replace(s)
}

// contactPhoneFromVCard pulls the first TEL value out of a vCard blob.
func contactPhoneFromVCard(vcard string) string {
	for _, line := range strings.Split(vcard, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "TEL") {
			if _, value, ok := strings.Cut(line, ":"); ok {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

// SendLocation shares a location pin (coordinates plus an optional place
// name/address).
func (c *Client) SendLocation(ctx context.Context, chatID string, lat, long float64, name, address string) (appstore.SavedTextMessage, error) {
	client, err := c.requireConnectedClient()
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	if lat < -90 || lat > 90 || long < -180 || long > 180 {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "coordinates out of range")
	}
	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()

	label := strings.TrimSpace(name)
	if label == "" {
		label = fmt.Sprintf("%.5f, %.5f", lat, long)
	}
	locationMsg := &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(lat),
		DegreesLongitude: proto.Float64(long),
	}
	if name = strings.TrimSpace(name); name != "" {
		locationMsg.Name = proto.String(name)
	}
	if address = strings.TrimSpace(address); address != "" {
		locationMsg.Address = proto.String(address)
	}
	messageID := client.GenerateMessageID()
	if _, err := client.SendMessage(ctx, targetJID, &waE2E.Message{LocationMessage: locationMsg}, whatsmeow.SendRequestExtra{ID: messageID}); err != nil {
		return appstore.SavedTextMessage{}, err
	}
	payload, _ := proto.Marshal(locationMsg)
	encoded, err := appstore.EncodePayload(appstore.MessagePayload{Location: &appstore.LocationPayload{
		Latitude:  lat,
		Longitude: long,
		Name:      strings.TrimSpace(name),
		Address:   strings.TrimSpace(address),
	}})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	saved, err := c.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, messageID),
			ChatID:      chatID,
			SenderID:    "me",
			Text:        label,
			Timestamp:   time.Now(),
			Direction:   appstore.DirectionOutgoing,
			Status:      appstore.StatusSent,
			IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
			CountUnread: false,
			PayloadJSON: encoded,
		},
		MediaKind:      appstore.MediaKindLocation,
		MediaMimeType:  "text/x-location",
		MediaPayload:   payload,
		PayloadSummary: label,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}
	if saved.Inserted {
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	return saved, nil
}
