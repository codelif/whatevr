package rpc

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/rpc/pb"
	appstore "whatevrd/internal/store"
)

type SendController interface {
	SendText(context.Context, string, string) (appstore.SavedTextMessage, error)
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
	if strings.TrimSpace(req.GetText()) == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}

	saved, err := s.sender.SendText(ctx, req.GetChatId(), req.GetText())
	if err != nil {
		return nil, err
	}

	return &pb.SendTextResponse{Message: toProtoMessage(toAppMessage(saved.Message))}, nil
}
