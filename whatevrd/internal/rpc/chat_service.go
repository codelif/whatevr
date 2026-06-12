package rpc

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
	appstore "whatevrd/internal/store"
)

const (
	defaultChatLimit    = 100
	maxChatLimit        = 500
	defaultMessageLimit = 50
	maxMessageLimit     = 200
)

type ChatStore interface {
	ListChats(context.Context, int, int, string) ([]appstore.Chat, error)
	ListMessages(context.Context, string, int, string) ([]appstore.Message, error)
	ListMessagesAround(context.Context, string, int, string) ([]appstore.Message, error)
	MarkChatRead(context.Context, string) (appstore.Chat, error)
}

type ChatActionController interface {
	MarkChatRead(context.Context, string) (appstore.Chat, error)
	RefreshLoadedChatAvatars(context.Context, string, []appstore.Message)
	SetChatPinned(context.Context, string, bool) (appstore.Chat, error)
	SetChatPresence(context.Context, string, bool) error
	SubscribeChatPresence(context.Context, string) error
	DownloadMessageMedia(context.Context, string) (appstore.Message, error)
	ResolveCachedStickerMedia(context.Context, []appstore.Message) []appstore.Message
	GetMessageInfo(context.Context, string) (app.MessageInfo, error)
	DeleteMessageForMe(context.Context, string) error
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

	limit, offset, err := normalizePage(req.GetLimit(), req.GetOffset(), defaultChatLimit, maxChatLimit)
	if err != nil {
		return nil, err
	}

	chats, err := s.store.ListChats(ctx, limit, offset, strings.TrimSpace(req.GetAfterChatId()))
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
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}
	limit, _, err := normalizePage(req.GetLimit(), 0, defaultMessageLimit, maxMessageLimit)
	if err != nil {
		return nil, err
	}

	var messages []appstore.Message
	aroundMessageID := strings.TrimSpace(req.GetAroundMessageId())
	if aroundMessageID != "" {
		messages, err = s.store.ListMessagesAround(ctx, req.GetChatId(), limit, aroundMessageID)
	} else {
		messages, err = s.store.ListMessages(ctx, req.GetChatId(), limit, req.GetBeforeMessageId())
	}
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

	// Lazily fetch the chat avatar plus the avatars of the senders in this page of
	// messages. Runs on the initial open and on every "load older" page, so newly
	// surfaced senders (e.g. group participants further back in history) get picked
	// up as the user scrolls. Already-cached subjects are skipped via the avatar TTL.
	if s.actions != nil {
		s.actions.RefreshLoadedChatAvatars(ctx, req.GetChatId(), messages)
	}

	return resp, nil
}

func (s *ChatService) MarkChatRead(ctx context.Context, req *pb.MarkChatReadRequest) (*pb.MarkChatReadResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
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

func (s *ChatService) SetChatPinned(ctx context.Context, req *pb.SetChatPinnedRequest) (*pb.SetChatPinnedResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	if _, err := s.actions.SetChatPinned(ctx, req.GetChatId(), req.GetPinned()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "chat not found")
		}
		return nil, err
	}
	return &pb.SetChatPinnedResponse{}, nil
}

func (s *ChatService) SetChatPresence(ctx context.Context, req *pb.SetChatPresenceRequest) (*pb.SetChatPresenceResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	if err := s.actions.SetChatPresence(ctx, req.GetChatId(), req.GetComposing()); err != nil {
		return nil, err
	}
	return &pb.SetChatPresenceResponse{}, nil
}

func (s *ChatService) SubscribeChatPresence(ctx context.Context, req *pb.SubscribeChatPresenceRequest) (*pb.SubscribeChatPresenceResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	if err := s.actions.SubscribeChatPresence(ctx, req.GetChatId()); err != nil {
		return nil, err
	}
	s.daemon.PublishCachedChatPresence(req.GetChatId())
	return &pb.SubscribeChatPresenceResponse{}, nil
}

func (s *ChatService) DownloadMessageMedia(ctx context.Context, req *pb.DownloadMessageMediaRequest) (*pb.DownloadMessageMediaResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}

	message, err := s.actions.DownloadMessageMedia(ctx, req.GetMessageId())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "message not found")
		}
		return nil, err
	}

	return &pb.DownloadMessageMediaResponse{Message: toProtoMessage(toAppMessage(message))}, nil
}

func (s *ChatService) GetMessageInfo(ctx context.Context, req *pb.GetMessageInfoRequest) (*pb.GetMessageInfoResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}

	info, err := s.actions.GetMessageInfo(ctx, strings.TrimSpace(req.GetMessageId()))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "message not found")
		}
		return nil, err
	}

	resp := &pb.GetMessageInfoResponse{
		Status:          toProtoMessageStatus(info.Status),
		SentTsUnix:      info.SentTsUnix,
		DeliveredTsUnix: info.DeliveredTsUnix,
		ReadTsUnix:      info.ReadTsUnix,
		IsGroup:         info.IsGroup,
		Receipts:        make([]*pb.ParticipantReceipt, 0, len(info.Receipts)),
	}
	for _, receipt := range info.Receipts {
		resp.Receipts = append(resp.Receipts, &pb.ParticipantReceipt{
			Jid:             receipt.JID,
			DisplayName:     receipt.DisplayName,
			AvatarLocalPath: receipt.AvatarLocalPath,
			DeliveredTsUnix: receipt.DeliveredTsUnix,
			ReadTsUnix:      receipt.ReadTsUnix,
			PlayedTsUnix:    receipt.PlayedTsUnix,
		})
	}
	return resp, nil
}

func (s *ChatService) DeleteMessageForMe(ctx context.Context, req *pb.DeleteMessageForMeRequest) (*pb.DeleteMessageForMeResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}

	if err := s.actions.DeleteMessageForMe(ctx, strings.TrimSpace(req.GetMessageId())); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "message not found")
		}
		return nil, err
	}
	return &pb.DeleteMessageForMeResponse{}, nil
}

func normalizePage(limit int32, offset int32, defaultLimit int, maxLimit int) (int, int, error) {
	if offset < 0 {
		return 0, 0, status.Error(codes.InvalidArgument, "offset must be non-negative")
	}
	if limit < 0 {
		return 0, 0, status.Error(codes.InvalidArgument, "limit must be non-negative")
	}
	if limit == 0 {
		limit = int32(defaultLimit)
	}
	if limit > int32(maxLimit) {
		return 0, 0, status.Errorf(codes.InvalidArgument, "limit must be <= %d", maxLimit)
	}
	return int(limit), int(offset), nil
}

func toAppChat(chat appstore.Chat) app.Chat {
	return app.Chat{
		ID:                   chat.ID,
		Name:                 chat.Name,
		LastMessage:          chat.LastMessage,
		LastMessageTime:      chat.LastMessageTime,
		LastMessageDirection: chat.LastMessageDirection,
		LastMessageStatus:    chat.LastMessageStatus,
		UnreadCount:          chat.UnreadCount,
		IsGroup:              chat.IsGroup,
		IsPinned:             chat.IsPinned,
		PinnedOrder:          chat.PinnedOrder,
		UpdatedAtUnix:        chat.UpdatedAt,
		AvatarLocalPath:      chat.AvatarLocalPath,
	}
}

func toAppMessage(message appstore.Message) app.Message {
	return app.Message{
		ID:                      message.ID,
		ChatID:                  message.ChatID,
		SenderID:                message.SenderID,
		SenderName:              message.SenderName,
		SenderAvatarLocalPath:   message.SenderAvatarLocalPath,
		Text:                    message.Text,
		TimestampUnix:           message.TimestampUnix,
		SortSeq:                 message.SortSeq,
		Direction:               message.Direction,
		Status:                  message.Status,
		MediaKind:               message.MediaKind,
		MediaMimeType:           message.MediaMimeType,
		MediaLocalPath:          message.MediaLocalPath,
		MediaThumbnailLocalPath: message.MediaThumbnailLocalPath,
		MediaWidth:              message.MediaWidth,
		MediaHeight:             message.MediaHeight,
		MediaAnimated:           message.MediaAnimated,
		MediaCacheKey:           message.MediaCacheKey,
		IsRevoked:               message.IsRevoked,
		ReplyTo: app.MessageReply{
			MessageID:     message.ReplyTo.MessageID,
			SenderID:      message.ReplyTo.SenderID,
			SenderName:    message.ReplyTo.SenderName,
			Text:          message.ReplyTo.Text,
			MediaKind:     message.ReplyTo.MediaKind,
			MediaMimeType: message.ReplyTo.MediaMimeType,
			Direction:     message.ReplyTo.Direction,
		},
		Reactions: toAppReactions(message.Reactions),
	}
}

func toAppReactions(reactions []appstore.Reaction) []app.Reaction {
	if len(reactions) == 0 {
		return nil
	}
	out := make([]app.Reaction, len(reactions))
	for i, reaction := range reactions {
		out[i] = app.Reaction{
			Emoji:         reaction.Emoji,
			SenderID:      reaction.SenderID,
			SenderName:    reaction.SenderName,
			TimestampUnix: reaction.TimestampUnix,
			FromMe:        reaction.FromMe,
		}
	}
	return out
}

func toAppAvatar(avatar appstore.Avatar) app.Avatar {
	kind := app.AvatarSubjectKindUnspecified
	switch avatar.SubjectKind {
	case appstore.AvatarSubjectChat:
		kind = app.AvatarSubjectKindChat
	case appstore.AvatarSubjectSender:
		kind = app.AvatarSubjectKindSender
	}
	return app.Avatar{Kind: kind, ID: avatar.SubjectID, LocalPath: avatar.LocalPath, Status: avatar.Status, UpdatedAtUnix: avatar.UpdatedAt, Fetching: avatar.Fetching}
}
