package rpc

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/rpc/pb"
	appstore "whatevrd/internal/store"
)

const (
	maxSendTextRunes  = 65536
	maxCaptionRunes   = 1024
	maxForwardTargets = 5
)

type SendController interface {
	SendText(context.Context, string, string, string) (appstore.SavedTextMessage, error)
	SendMedia(context.Context, string, string, string, string) (appstore.SavedTextMessage, error)
	RevokeMessage(context.Context, string) (appstore.Message, error)
	ForwardMessage(context.Context, string, []string) ([]appstore.SavedTextMessage, error)
	SendReaction(context.Context, string, string) (appstore.Message, error)
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

func (s *SendService) RevokeMessage(ctx context.Context, req *pb.RevokeMessageRequest) (*pb.RevokeMessageResponse, error) {
	if s.sender == nil {
		return nil, status.Error(codes.Unimplemented, "send controller is not available")
	}
	if strings.TrimSpace(req.GetMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}

	message, err := s.sender.RevokeMessage(ctx, strings.TrimSpace(req.GetMessageId()))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "message not found")
		}
		return nil, err
	}
	return &pb.RevokeMessageResponse{Message: toProtoMessage(toAppMessage(message))}, nil
}

func (s *SendService) SendReaction(ctx context.Context, req *pb.SendReactionRequest) (*pb.SendReactionResponse, error) {
	if s.sender == nil {
		return nil, status.Error(codes.Unimplemented, "send controller is not available")
	}
	if strings.TrimSpace(req.GetMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}

	message, err := s.sender.SendReaction(ctx, strings.TrimSpace(req.GetMessageId()), req.GetEmoji())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "message not found")
		}
		return nil, err
	}
	return &pb.SendReactionResponse{Message: toProtoMessage(toAppMessage(message))}, nil
}

func (s *SendService) ForwardMessage(ctx context.Context, req *pb.ForwardMessageRequest) (*pb.ForwardMessageResponse, error) {
	if s.sender == nil {
		return nil, status.Error(codes.Unimplemented, "send controller is not available")
	}
	if strings.TrimSpace(req.GetSourceMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "source_message_id is required")
	}

	targets := make([]string, 0, len(req.GetTargetChatIds()))
	seen := make(map[string]bool, len(req.GetTargetChatIds()))
	for _, target := range req.GetTargetChatIds() {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one target_chat_id is required")
	}
	if len(targets) > maxForwardTargets {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d target chats per forward", maxForwardTargets)
	}

	saved, err := s.sender.ForwardMessage(ctx, strings.TrimSpace(req.GetSourceMessageId()), targets)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "message not found")
		}
		return nil, err
	}

	resp := &pb.ForwardMessageResponse{Messages: make([]*pb.Message, 0, len(saved))}
	for _, result := range saved {
		resp.Messages = append(resp.Messages, toProtoMessage(toAppMessage(result.Message)))
	}
	return resp, nil
}
