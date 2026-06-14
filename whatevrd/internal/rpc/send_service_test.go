package rpc

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/rpc/pb"
	appstore "whatevrd/internal/store"
)

type fakeSendController struct {
	text          string
	mentionedJIDs []string
}

func (f *fakeSendController) SendText(_ context.Context, chatID, text, _ string, mentionedJIDs []string) (appstore.SavedTextMessage, error) {
	f.text = text
	f.mentionedJIDs = mentionedJIDs
	return appstore.SavedTextMessage{
		Message: appstore.Message{
			ID:        chatID + ":message-id",
			ChatID:    chatID,
			SenderID:  "me",
			Text:      text,
			Direction: appstore.DirectionOutgoing,
			Status:    appstore.StatusPending,
		},
	}, nil
}

func (f *fakeSendController) SendMedia(context.Context, string, string, string, string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{}, nil
}

func (f *fakeSendController) RevokeMessage(context.Context, string) (appstore.Message, error) {
	return appstore.Message{}, nil
}

func (f *fakeSendController) EditMessage(context.Context, string, string) (appstore.Message, error) {
	return appstore.Message{}, nil
}

func (f *fakeSendController) ForwardMessage(context.Context, string, []string) ([]appstore.SavedTextMessage, error) {
	return nil, nil
}

func (f *fakeSendController) SendReaction(context.Context, string, string) (appstore.Message, error) {
	return appstore.Message{}, nil
}

func (f *fakeSendController) SetMessageStarred(context.Context, string, bool) (appstore.Message, error) {
	return appstore.Message{}, nil
}

func (f *fakeSendController) PinMessage(context.Context, string, bool, uint32) (appstore.Message, error) {
	return appstore.Message{}, nil
}

func TestSendTextAllowsNativeSizedMessage(t *testing.T) {
	sender := &fakeSendController{}
	service := NewSendService(sender)
	text := strings.Repeat("a", maxSendTextRunes)

	resp, err := service.SendText(context.Background(), &pb.SendTextRequest{ChatId: "user@s.whatsapp.net", Text: text})
	if err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}
	if sender.text != text {
		t.Fatalf("sent text length = %d, want %d", len(sender.text), len(text))
	}
	if resp.GetMessage().GetText() != text {
		t.Fatalf("response text length = %d, want %d", len(resp.GetMessage().GetText()), len(text))
	}
}

func TestSendTextRejectsExcessiveMessage(t *testing.T) {
	service := NewSendService(&fakeSendController{})
	text := strings.Repeat("a", maxSendTextRunes+1)

	_, err := service.SendText(context.Background(), &pb.SendTextRequest{ChatId: "user@s.whatsapp.net", Text: text})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SendText status = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}
