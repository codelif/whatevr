package rpc

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

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
	defaultSearchLimit  = 30
	maxSearchLimit      = 100
)

type ChatStore interface {
	ListChats(context.Context, int, int, string) ([]appstore.Chat, error)
	ListMessages(context.Context, string, int, string) ([]appstore.Message, error)
	ListMessagesAround(context.Context, string, int, string) ([]appstore.Message, error)
	ListMessagesAroundUnread(context.Context, string, int, int) ([]appstore.Message, string, error)
	ListStarredMessages(context.Context, string, int, string) ([]appstore.StarredMessage, error)
	ListPinnedMessages(context.Context, string) ([]appstore.Message, error)
	SearchChats(context.Context, string, int) ([]appstore.Chat, error)
	SearchMessages(context.Context, string, string, int, string) ([]appstore.MessageSearchResult, error)
	MarkChatRead(context.Context, string) (appstore.Chat, error)
}

type ChatActionController interface {
	MarkChatRead(context.Context, string) (appstore.Chat, error)
	RefreshLoadedChatAvatars(context.Context, string, []appstore.Message)
	SetChatPinned(context.Context, string, bool) (appstore.Chat, error)
	SetChatArchived(context.Context, string, bool) (appstore.Chat, error)
	SetChatMuted(context.Context, string, bool, time.Duration) (appstore.Chat, error)
	SetChatPresence(context.Context, string, bool) error
	SubscribeChatPresence(context.Context, string) error
	DownloadMessageMedia(context.Context, string) (appstore.Message, error)
	ResolveCachedStickerMedia(context.Context, []appstore.Message) []appstore.Message
	FillReactionSenderNames(context.Context, []appstore.Message) []appstore.Message
	GetMessageInfo(context.Context, string) (app.MessageInfo, error)
	DeleteMessageForMe(context.Context, string) error
	CheckPhoneOnWhatsApp(context.Context, string) (app.PhoneCheck, error)
	EnsureDirectChat(context.Context, string) (appstore.Chat, error)
	GetContactInfo(context.Context, string) (app.ContactInfo, error)
	SelfProfile(context.Context) (app.ContactInfo, error)
	GetGroupInfo(context.Context, string) (app.GroupInfo, error)
	FetchProfilePicture(context.Context, string) (string, error)
	RequestAvatars(context.Context, []appstore.AvatarSubject, bool) []app.Avatar
	RequestOlderMessages(context.Context, string) (bool, error)
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
	unreadAnchorMessageID := ""
	aroundUnreadCount := int(req.GetAroundUnreadCount())
	aroundMessageID := strings.TrimSpace(req.GetAroundMessageId())
	if aroundUnreadCount > 0 {
		messages, unreadAnchorMessageID, err = s.store.ListMessagesAroundUnread(ctx, req.GetChatId(), limit, aroundUnreadCount)
	} else if aroundMessageID != "" {
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
	// Reactions synced from history may predate sender-name resolution; heal
	// them before serialization so the UI never shows a placeholder reactor.
	if s.actions != nil {
		messages = s.actions.FillReactionSenderNames(ctx, messages)
	}

	resp := &pb.GetMessagesResponse{
		Messages:              make([]*pb.Message, 0, len(messages)),
		UnreadAnchorMessageId: unreadAnchorMessageID,
	}
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

func (s *ChatService) RequestOlderMessages(ctx context.Context, req *pb.RequestOlderMessagesRequest) (*pb.RequestOlderMessagesResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	requested, err := s.actions.RequestOlderMessages(ctx, req.GetChatId())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "chat not found")
		}
		return nil, err
	}
	return &pb.RequestOlderMessagesResponse{Requested: requested}, nil
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

func (s *ChatService) SetChatArchived(ctx context.Context, req *pb.SetChatArchivedRequest) (*pb.SetChatArchivedResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	if _, err := s.actions.SetChatArchived(ctx, req.GetChatId(), req.GetArchived()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "chat not found")
		}
		return nil, err
	}
	return &pb.SetChatArchivedResponse{}, nil
}

func (s *ChatService) SetChatMuted(ctx context.Context, req *pb.SetChatMutedRequest) (*pb.SetChatMutedResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "chat action controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	duration := time.Duration(req.GetMuteDurationSecs()) * time.Second
	if _, err := s.actions.SetChatMuted(ctx, req.GetChatId(), req.GetMuted(), duration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "chat not found")
		}
		return nil, err
	}
	return &pb.SetChatMutedResponse{}, nil
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

func (s *ChatService) ListStarredMessages(ctx context.Context, req *pb.ListStarredMessagesRequest) (*pb.ListStarredMessagesResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}
	limit, _, err := normalizePage(req.GetLimit(), 0, defaultMessageLimit, maxMessageLimit)
	if err != nil {
		return nil, err
	}

	items, err := s.store.ListStarredMessages(ctx, strings.TrimSpace(req.GetChatId()), limit, strings.TrimSpace(req.GetBeforeMessageId()))
	if err != nil {
		return nil, err
	}

	resp := &pb.ListStarredMessagesResponse{Items: make([]*pb.StarredMessageItem, 0, len(items))}
	for _, item := range items {
		resp.Items = append(resp.Items, &pb.StarredMessageItem{
			Message:  toProtoMessage(toAppMessage(item.Message)),
			ChatName: item.ChatName,
		})
	}
	resp.HasMore = len(items) == limit
	return resp, nil
}

func (s *ChatService) ListPinnedMessages(ctx context.Context, req *pb.ListPinnedMessagesRequest) (*pb.ListPinnedMessagesResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}

	messages, err := s.store.ListPinnedMessages(ctx, strings.TrimSpace(req.GetChatId()))
	if err != nil {
		return nil, err
	}

	resp := &pb.ListPinnedMessagesResponse{Messages: make([]*pb.Message, 0, len(messages))}
	for _, message := range messages {
		resp.Messages = append(resp.Messages, toProtoMessage(toAppMessage(message)))
	}
	return resp, nil
}

func (s *ChatService) SearchChats(ctx context.Context, req *pb.SearchChatsRequest) (*pb.SearchChatsResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}
	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return &pb.SearchChatsResponse{}, nil
	}
	limit, _, err := normalizePage(req.GetLimit(), 0, defaultSearchLimit, maxSearchLimit)
	if err != nil {
		return nil, err
	}

	chats, err := s.store.SearchChats(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	resp := &pb.SearchChatsResponse{Chats: make([]*pb.Chat, 0, len(chats))}
	for _, chat := range chats {
		resp.Chats = append(resp.Chats, toProtoChat(toAppChat(chat)))
	}
	return resp, nil
}

func (s *ChatService) SearchMessages(ctx context.Context, req *pb.SearchMessagesRequest) (*pb.SearchMessagesResponse, error) {
	if s.store == nil {
		return nil, status.Error(codes.Unimplemented, "chat store is not available")
	}
	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return &pb.SearchMessagesResponse{}, nil
	}
	limit, _, err := normalizePage(req.GetLimit(), 0, defaultSearchLimit, maxSearchLimit)
	if err != nil {
		return nil, err
	}

	results, err := s.store.SearchMessages(ctx, query, strings.TrimSpace(req.GetChatId()), limit, strings.TrimSpace(req.GetBeforeMessageId()))
	if err != nil {
		return nil, err
	}

	resp := &pb.SearchMessagesResponse{Results: make([]*pb.MessageSearchResult, 0, len(results))}
	for _, result := range results {
		resp.Results = append(resp.Results, &pb.MessageSearchResult{
			Message:  toProtoMessage(toAppMessage(result.Message)),
			ChatName: result.ChatName,
		})
	}
	resp.HasMore = len(results) == limit
	return resp, nil
}

func (s *ChatService) CheckPhoneOnWhatsApp(ctx context.Context, req *pb.CheckPhoneOnWhatsAppRequest) (*pb.CheckPhoneOnWhatsAppResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "contact lookup is not available")
	}
	if strings.TrimSpace(req.GetPhone()) == "" {
		return nil, status.Error(codes.InvalidArgument, "phone is required")
	}
	check, err := s.actions.CheckPhoneOnWhatsApp(ctx, req.GetPhone())
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.CheckPhoneOnWhatsAppResponse{
		Registered:  check.Registered,
		Jid:         check.JID,
		DisplayName: check.DisplayName,
		IsBusiness:  check.IsBusiness,
		Phone:       check.Phone,
	}, nil
}

func (s *ChatService) EnsureDirectChat(ctx context.Context, req *pb.EnsureDirectChatRequest) (*pb.EnsureDirectChatResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "contact lookup is not available")
	}
	if strings.TrimSpace(req.GetJid()) == "" {
		return nil, status.Error(codes.InvalidArgument, "jid is required")
	}
	chat, err := s.actions.EnsureDirectChat(ctx, req.GetJid())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.EnsureDirectChatResponse{Chat: toProtoChat(toAppChat(chat))}, nil
}

func (s *ChatService) GetContactInfo(ctx context.Context, req *pb.GetContactInfoRequest) (*pb.GetContactInfoResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "contact lookup is not available")
	}
	if strings.TrimSpace(req.GetJid()) == "" {
		return nil, status.Error(codes.InvalidArgument, "jid is required")
	}
	info, err := s.actions.GetContactInfo(ctx, req.GetJid())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.GetContactInfoResponse{
		Jid:             info.JID,
		PhoneNumber:     info.PhoneNumber,
		SavedName:       info.SavedName,
		PushName:        info.PushName,
		BusinessName:    info.BusinessName,
		AvatarLocalPath: info.AvatarLocalPath,
		IsBusiness:      info.IsBusiness,
		StatusText:      info.StatusText,
	}, nil
}

func (s *ChatService) GetSelfProfile(ctx context.Context, req *pb.GetSelfProfileRequest) (*pb.GetSelfProfileResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "profile lookup is not available")
	}
	info, err := s.actions.SelfProfile(ctx)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &pb.GetSelfProfileResponse{
		Jid:             info.JID,
		PhoneNumber:     info.PhoneNumber,
		PushName:        info.PushName,
		AvatarLocalPath: info.AvatarLocalPath,
		StatusText:      info.StatusText,
	}, nil
}

func (s *ChatService) GetGroupInfo(ctx context.Context, req *pb.GetGroupInfoRequest) (*pb.GetGroupInfoResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "group lookup is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}
	info, err := s.actions.GetGroupInfo(ctx, req.GetChatId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	resp := &pb.GetGroupInfoResponse{
		Subject:         info.Subject,
		Description:     info.Description,
		AvatarLocalPath: info.AvatarLocalPath,
		CreatedUnix:     info.CreatedUnix,
		Members:         make([]*pb.GroupMember, 0, len(info.Members)),
	}
	for _, member := range info.Members {
		resp.Members = append(resp.Members, &pb.GroupMember{
			Jid:             member.JID,
			DisplayName:     member.DisplayName,
			PhoneNumber:     member.PhoneNumber,
			AvatarLocalPath: member.AvatarLocalPath,
			IsAdmin:         member.IsAdmin,
			IsSuperAdmin:    member.IsSuperAdmin,
		})
	}
	return resp, nil
}

func (s *ChatService) FetchProfilePicture(ctx context.Context, req *pb.FetchProfilePictureRequest) (*pb.FetchProfilePictureResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "profile pictures are not available")
	}
	if strings.TrimSpace(req.GetJid()) == "" {
		return nil, status.Error(codes.InvalidArgument, "jid is required")
	}
	localPath, err := s.actions.FetchProfilePicture(ctx, req.GetJid())
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.FetchProfilePictureResponse{LocalPath: localPath}, nil
}

const maxAvatarRequestRefs = 200

func (s *ChatService) RequestAvatars(ctx context.Context, req *pb.RequestAvatarsRequest) (*pb.RequestAvatarsResponse, error) {
	if s.actions == nil {
		return nil, status.Error(codes.Unimplemented, "avatars are not available")
	}
	refs := req.GetRefs()
	if len(refs) == 0 {
		return &pb.RequestAvatarsResponse{}, nil
	}
	if len(refs) > maxAvatarRequestRefs {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d refs per request", maxAvatarRequestRefs)
	}

	subjects := make([]appstore.AvatarSubject, 0, len(refs))
	for _, ref := range refs {
		var kind string
		switch ref.GetKind() {
		case pb.AvatarSubjectKind_AVATAR_SUBJECT_KIND_CHAT:
			kind = appstore.AvatarSubjectChat
		case pb.AvatarSubjectKind_AVATAR_SUBJECT_KIND_SENDER:
			kind = appstore.AvatarSubjectSender
		default:
			continue
		}
		subjects = append(subjects, appstore.AvatarSubject{Kind: kind, ID: ref.GetId()})
	}

	background := req.GetPriority() == pb.AvatarPriority_AVATAR_PRIORITY_BACKGROUND
	avatars := s.actions.RequestAvatars(ctx, subjects, background)
	resp := &pb.RequestAvatarsResponse{Avatars: make([]*pb.Avatar, 0, len(avatars))}
	for _, avatar := range avatars {
		resp.Avatars = append(resp.Avatars, toProtoAvatar(avatar))
	}
	return resp, nil
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
		IsArchived:           chat.IsArchived,
		IsMuted:              chat.IsMuted,
		MuteEndTimestamp:     chat.MuteEndTimestamp,
		HistoryExhausted:     chat.HistoryExhausted,
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
		IsEdited:                message.IsEdited,
		IsStarred:               message.IsStarred,
		PinnedUntilUnix:         message.PinnedUntil,
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
		Mentions:  toAppMentions(message.Mentions),
	}
}

func toAppMentions(mentions []appstore.MessageMention) []app.Mention {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]app.Mention, len(mentions))
	for i, mention := range mentions {
		out[i] = app.Mention{JID: mention.JID, DisplayName: mention.DisplayName}
	}
	return out
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
