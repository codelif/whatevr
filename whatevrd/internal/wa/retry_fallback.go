package wa

import (
	"context"
	"database/sql"
	"errors"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	appstore "whatevrd/internal/store"
)

// retryFallbackBuffer layers the daemon's own message store under whatsmeow's
// persistent retry buffer. When a recipient sends a retry receipt ("waiting
// for this message" on their side) whatsmeow answers from its retry buffer;
// if that buffer no longer holds the message (past its 7-day retention, or a
// rebuilt session dir), the daemon rebuilds the sent proto from the messages
// table so the recipient still gets the content instead of a permanent
// placeholder.
type retryFallbackBuffer struct {
	store.EventBuffer
	client *Client
}

func (b *retryFallbackBuffer) GetOutgoingEvent(ctx context.Context, chatJID, altChatJID types.JID, id types.MessageID) (string, []byte, error) {
	format, payload, err := b.EventBuffer.GetOutgoingEvent(ctx, chatJID, altChatJID, id)
	if err == nil && len(payload) > 0 {
		return format, payload, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// A real storage failure, not a miss — surface it untouched.
		return format, payload, err
	}

	for _, jid := range []types.JID{chatJID, altChatJID} {
		if jid.IsEmpty() {
			continue
		}
		message, dbErr := b.client.store.GetMessage(ctx, internalMessageIDForChat(jid.ToNonAD().String(), id))
		if dbErr != nil || message.Direction != appstore.DirectionOutgoing {
			continue
		}
		rebuilt := b.client.rebuildOutgoingMessageForRetry(ctx, message)
		if rebuilt == nil {
			continue
		}
		encoded, marshalErr := proto.Marshal(rebuilt)
		if marshalErr != nil {
			continue
		}
		b.client.log.Infof("Answered retry for %s/%s from the daemon message store", jid.ToNonAD(), id)
		return "wa", encoded, nil
	}

	if err == nil {
		err = sql.ErrNoRows
	}
	return "", nil, err
}

// rebuildOutgoingMessageForRetry reconstructs the waE2E proto the send queue
// originally put on the wire (sendPendingMessage and friends). Only the shapes
// the daemon itself originates are covered — plain/extended text, images and
// stickers (whose sent protos are persisted as media_payload after a
// successful send); anything else returns nil and stays a buffer miss.
func (c *Client) rebuildOutgoingMessageForRetry(ctx context.Context, message appstore.Message) *waE2E.Message {
	contextInfo := c.outgoingContextInfo(ctx, c.currentClient(), message)

	switch message.MediaKind {
	case appstore.MediaKindSticker:
		sticker := &waE2E.StickerMessage{}
		if len(message.MediaPayload) == 0 || proto.Unmarshal(message.MediaPayload, sticker) != nil || sticker.GetDirectPath() == "" {
			return nil
		}
		if contextInfo != nil {
			sticker.ContextInfo = contextInfo
		}
		return &waE2E.Message{StickerMessage: sticker}
	case appstore.MediaKindImage:
		img := &waE2E.ImageMessage{}
		if len(message.MediaPayload) == 0 || proto.Unmarshal(message.MediaPayload, img) != nil || img.GetDirectPath() == "" {
			return nil
		}
		if message.Text != "" {
			img.Caption = proto.String(message.Text)
		}
		if contextInfo != nil {
			img.ContextInfo = contextInfo
		}
		return &waE2E.Message{ImageMessage: img}
	case "":
		if message.MediaMimeType != "" || message.MediaLocalPath != "" || message.Text == "" {
			return nil
		}
		if contextInfo != nil {
			return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(message.Text),
				ContextInfo: contextInfo,
			}}
		}
		return &waE2E.Message{Conversation: proto.String(message.Text)}
	default:
		return nil
	}
}
