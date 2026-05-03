package rpc

import (
	"context"
	"database/sql"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
	appstore "whatevrd/internal/store"
)

type ChatStore interface {
	ListChats(context.Context, int, int) ([]appstore.Chat, error)
	ListMessages(context.Context, string, int, string) ([]appstore.Message, error)
	MarkChatRead(context.Context, string) (appstore.Chat, error)
}

type ChatActionController interface {
	MarkChatRead(context.Context, string) (appstore.Chat, error)
	SetChatPresence(context.Context, string, bool) error
}

type ChatService struct {
	pb.UnimplementedChatServiceServer
	daemon  *app.Daemon
	store   ChatStore
	actions ChatActionController
}

func NewChatService(daemon *app.Daemon, store ChatStore, actions ChatActionController) *ChatService {
	return &ChatService{daemon: daemon, store: store, actions: actions}
}

func (s *ChatService) ListChats(ctx context.Context, req *pb.ListChatsRequest) (*pb.ListChatsResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}

	chats, err := s.store.ListChats(ctx, int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, err
	}

	resp := &pb.ListChatsResponse{Chats: make([]*pb.Chat, 0, len(chats))}
	for _, chat := range chats {
		resp.Chats = append(resp.Chats, toProtoChat(toAppChat(chat)))
	}

	return resp, nil
}

func (s *ChatService) GetMessages(ctx context.Context, req *pb.GetMessagesRequest) (*pb.GetMessagesResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}
	if req.GetChatId() == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	messages, err := s.store.ListMessages(ctx, req.GetChatId(), int(req.GetLimit()), req.GetBeforeMessageId())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "message not found")
		}
		return nil, err
	}

	resp := &pb.GetMessagesResponse{Messages: make([]*pb.Message, 0, len(messages))}
	for _, message := range messages {
		resp.Messages = append(resp.Messages, toProtoMessage(toAppMessage(message)))
	}

	return resp, nil
}

func (s *ChatService) MarkChatRead(ctx context.Context, req *pb.MarkChatReadRequest) (*pb.MarkChatReadResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}
	if req.GetChatId() == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	markRead := s.store.MarkChatRead
	publishUpdate := true
	if s.actions != nil {
		markRead = s.actions.MarkChatRead
		publishUpdate = false
	}

	chat, err := markRead(ctx, req.GetChatId())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "chat not found")
		}
		return nil, err
	}

	if publishUpdate {
		s.daemon.PublishChatUpdated(toAppChat(chat))
	}
	return &pb.MarkChatReadResponse{}, nil
}

func (s *ChatService) SetChatPresence(ctx context.Context, req *pb.SetChatPresenceRequest) (*pb.SetChatPresenceResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if req.GetChatId() == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	if err := s.actions.SetChatPresence(ctx, req.GetChatId(), req.GetComposing()); err != nil {
		return nil, err
	}
	return &pb.SetChatPresenceResponse{}, nil
}

func toAppChat(chat appstore.Chat) app.Chat {
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

func toAppMessage(message appstore.Message) app.Message {
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
