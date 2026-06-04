package rpc

import (
	"context"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/rpc/pb"
	appstore "whatevrd/internal/store"
)

const (
	maxSendTextRunes = 4096
	maxCaptionRunes  = 1024
)

type SendController interface {
	SendText(context.Context, string, string, string) (appstore.SavedTextMessage, error)
	SendMedia(context.Context, string, string, string, string) (appstore.SavedTextMessage, error)
}

type SendService struct {
	pb.UnimplementedSendServiceServer
	sender SendController
}

func NewSendService(sender SendController) *SendService {
	return &SendService{sender: sender}
}

func (s *SendService) SendText(ctx context.Context, req *pb.SendTextRequest) (*pb.SendTextResponse, error) {
	if s.sender == nil {
		return nil, status.Error(codes.Unimplemented, "send controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}
	text := strings.TrimSpace(req.GetText())
	if text == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}
	if utf8.RuneCountInString(text) > maxSendTextRunes {
		return nil, status.Errorf(codes.InvalidArgument, "text must be <= %d characters", maxSendTextRunes)
	}

	saved, err := s.sender.SendText(ctx, strings.TrimSpace(req.GetChatId()), text, strings.TrimSpace(req.GetReplyToMessageId()))
	if err != nil {
		return nil, err
	}

	return &pb.SendTextResponse{Message: toProtoMessage(toAppMessage(saved.Message))}, nil
}

func (s *SendService) SendMedia(ctx context.Context, req *pb.SendMediaRequest) (*pb.SendMediaResponse, error) {
	if s.sender == nil {
		return nil, status.Error(codes.Unimplemented, "send controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}
	filePath := strings.TrimSpace(req.GetFilePath())
	if filePath == "" {
		return nil, status.Error(codes.InvalidArgument, "file_path is required")
	}
	caption := strings.TrimSpace(req.GetCaption())
	if utf8.RuneCountInString(caption) > maxCaptionRunes {
		return nil, status.Errorf(codes.InvalidArgument, "caption must be <= %d characters", maxCaptionRunes)
	}

	saved, err := s.sender.SendMedia(ctx, strings.TrimSpace(req.GetChatId()), filePath, caption, strings.TrimSpace(req.GetReplyToMessageId()))
	if err != nil {
		return nil, err
	}

	return &pb.SendMediaResponse{Message: toProtoMessage(toAppMessage(saved.Message))}, nil
}
