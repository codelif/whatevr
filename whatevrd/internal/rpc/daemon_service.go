package rpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

// Version is the daemon's reported version. It defaults to "dev" and is
// overridden at build time via -ldflags "-X whatevrd/internal/rpc.Version=...".
// It must remain a var (not a const) for the linker override to take effect.
var Version = "dev"

type ReconnectController interface {
	Reconnect(context.Context) error
}

type DaemonService struct {
	pb.UnimplementedDaemonServiceServer
	daemon      *app.Daemon
	reconnector ReconnectController
	shutdown    <-chan struct{}
}

func NewDaemonService(daemon *app.Daemon, reconnector ReconnectController, shutdown <-chan struct{}) *DaemonService {
	return &DaemonService{daemon: daemon, reconnector: reconnector, shutdown: shutdown}
}

func (s *DaemonService) Reconnect(ctx context.Context, _ *pb.ReconnectRequest) (*pb.ReconnectResponse, error) {
	if s.reconnector == nil {
		return nil, status.Error(codes.Unimplemented, "reconnect not available")
	}
	if err := s.reconnector.Reconnect(ctx); err != nil {
		return nil, err
	}
	return &pb.ReconnectResponse{}, nil
}

func (s *DaemonService) GetStatus(context.Context, *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	status := s.daemon.Status()
	detail := status.Detail
	if status.DroppedDaemonEvents > 0 || status.DroppedLoginEvents > 0 {
		resyncDetail := fmt.Sprintf("Some frontend events were dropped; refresh to resync. daemon=%d login=%d", status.DroppedDaemonEvents, status.DroppedLoginEvents)
		if detail == "" {
			detail = resyncDetail
		} else {
			detail += "\n" + resyncDetail
		}
	}
	return &pb.GetStatusResponse{
		State:         toProtoState(status.State),
		StateLabel:    status.State.String(),
		SocketPath:    status.Paths.SocketPath,
		DataDir:       status.Paths.DataDir,
		CacheDir:      status.Paths.CacheDir,
		DatabasePath:  status.Paths.DatabasePath,
		Version:       Version,
		Detail:        detail,
		CanReconnect:  status.CanReconnect,
		RetryAttempt:  status.RetryAttempt,
		NextRetryUnix: status.NextRetryUnix,
	}, nil
}

func (s *DaemonService) SubscribeEvents(_ *pb.SubscribeEventsRequest, stream pb.DaemonService_SubscribeEventsServer) error {
	events, cancel := s.daemon.SubscribeDaemonEvents()
	defer cancel()

	for {
		select {
		case event := <-events:
			protoEvent := toProtoDaemonEvent(event)
			if protoEvent == nil {
				continue
			}

			if err := stream.Send(protoEvent); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return nil
		case <-s.shutdown:
			// Daemon is shutting down: return promptly so GracefulStop does
			// not have to wait out the timeout for this long-lived stream.
			return nil
		}
	}
}

func toProtoDaemonEvent(event app.DaemonEvent) *pb.DaemonEvent {
	switch event.Kind {
	case app.DaemonEventConnectionChanged:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_ConnectionChanged{
				ConnectionChanged: &pb.ConnectionChanged{
					State:         toProtoState(event.State),
					Detail:        event.Detail,
					Retrying:      event.RetryAttempt > 0,
					RetryAttempt:  event.RetryAttempt,
					NextRetryUnix: event.NextRetryUnix,
					CanReconnect:  event.CanReconnect,
				},
			},
		}
	case app.DaemonEventNewMessage:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_NewMessage{
				NewMessage: &pb.NewMessage{Message: toProtoMessage(event.Message)},
			},
		}
	case app.DaemonEventMessageUpdated:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_MessageUpdated{
				MessageUpdated: &pb.MessageUpdated{Message: toProtoMessage(event.Message)},
			},
		}
	case app.DaemonEventChatUpdated:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_ChatUpdated{
				ChatUpdated: &pb.ChatUpdated{Chat: toProtoChat(event.Chat), PreviousChatId: event.PreviousChatID},
			},
		}
	case app.DaemonEventMessageDeleted:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_MessageDeleted{
				MessageDeleted: &pb.MessageDeleted{ChatId: event.DeletedChatID, MessageId: event.DeletedMessageID},
			},
		}
	case app.DaemonEventChatPresence:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_ChatPresenceChanged{
				ChatPresenceChanged: &pb.ChatPresenceChanged{
					ChatId:       event.Chat.ID,
					SenderId:     event.SenderID,
					IsComposing:  event.IsComposing,
					Availability: toProtoContactAvailability(event.Availability),
					LastSeenUnix: event.LastSeenUnix,
				},
			},
		}
	case app.DaemonEventHistorySyncProgress:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_HistorySyncProgress{
				HistorySyncProgress: &pb.HistorySyncProgress{
					SyncType:               toProtoHistorySyncType(event.HistorySync.SyncType),
					ProgressPercent:        event.HistorySync.ProgressPercent,
					ChunkOrder:             event.HistorySync.ChunkOrder,
					ConversationsInChunk:   event.HistorySync.ConversationsInChunk,
					MessagesInChunk:        event.HistorySync.MessagesInChunk,
					IsComplete:             event.HistorySync.IsComplete,
					Phase:                  toProtoHistorySyncPhase(event.HistorySync.Phase),
					ProcessedConversations: event.HistorySync.ProcessedConversations,
					ProcessedMessages:      event.HistorySync.ProcessedMessages,
				},
			},
		}
	case app.DaemonEventHistoryBackfilled:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_HistoryBackfilled{
				HistoryBackfilled: &pb.HistoryBackfilled{
					ChatId:        event.HistorySync.ChatID,
					MessagesAdded: event.HistorySync.MessagesAdded,
				},
			},
		}
	case app.DaemonEventMediaDownloadChanged:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_MediaDownloadChanged{
				MediaDownloadChanged: &pb.MediaDownloadChanged{
					MessageId:     event.MediaDownload.MessageID,
					ChatId:        event.MediaDownload.ChatID,
					Downloading:   event.MediaDownload.Downloading,
					ErrorText:     event.MediaDownload.ErrorText,
					ReceivedBytes: event.MediaDownload.ReceivedBytes,
					TotalBytes:    event.MediaDownload.TotalBytes,
				},
			},
		}
	case app.DaemonEventAvatarUpdated:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_AvatarUpdated{
				AvatarUpdated: &pb.AvatarUpdated{Avatar: toProtoAvatar(event.Avatar)},
			},
		}
	case app.DaemonEventStickerLibraryChanged:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_StickerLibraryChanged{
				StickerLibraryChanged: &pb.StickerLibraryChanged{Source: toProtoStickerSource(event.StickerSource)},
			},
		}
	case app.DaemonEventStickerDownloadChanged:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_StickerDownloadChanged{
				StickerDownloadChanged: &pb.StickerDownloadChanged{
					Sticker:   toProtoSticker(event.StickerDownload.Sticker),
					ErrorText: event.StickerDownload.ErrorText,
				},
			},
		}
	default:
		return nil
	}
}

func toProtoSticker(sticker app.Sticker) *pb.Sticker {
	return &pb.Sticker{
		CacheKey:          sticker.CacheKey,
		LocalPath:         sticker.LocalPath,
		MimeType:          sticker.MimeType,
		IsAnimated:        sticker.IsAnimated,
		Width:             sticker.Width,
		Height:            sticker.Height,
		Emojis:            sticker.Emojis,
		AccessibilityText: sticker.AccessibilityText,
		PackId:            sticker.PackID,
		IsFavorite:        sticker.IsFavorite,
		LastUsedUnix:      sticker.LastUsedUnix,
		Weight:            sticker.Weight,
	}
}

func toProtoStickerSource(source app.StickerSource) pb.StickerSource {
	switch source {
	case app.StickerSourceRecent:
		return pb.StickerSource_STICKER_SOURCE_RECENT
	case app.StickerSourceFavorite:
		return pb.StickerSource_STICKER_SOURCE_FAVORITE
	case app.StickerSourceAll:
		return pb.StickerSource_STICKER_SOURCE_ALL
	default:
		return pb.StickerSource_STICKER_SOURCE_UNSPECIFIED
	}
}

func toProtoAvatar(avatar app.Avatar) *pb.Avatar {
	return &pb.Avatar{
		Kind:          toProtoAvatarSubjectKind(avatar.Kind),
		Id:            avatar.ID,
		LocalPath:     avatar.LocalPath,
		Status:        avatar.Status,
		UpdatedAtUnix: avatar.UpdatedAtUnix,
		Fetching:      avatar.Fetching,
	}
}

func toProtoAvatarSubjectKind(kind app.AvatarSubjectKind) pb.AvatarSubjectKind {
	switch kind {
	case app.AvatarSubjectKindChat:
		return pb.AvatarSubjectKind_AVATAR_SUBJECT_KIND_CHAT
	case app.AvatarSubjectKindSender:
		return pb.AvatarSubjectKind_AVATAR_SUBJECT_KIND_SENDER
	default:
		return pb.AvatarSubjectKind_AVATAR_SUBJECT_KIND_UNSPECIFIED
	}
}

func toProtoContactAvailability(availability app.ContactAvailability) pb.ContactAvailability {
	switch availability {
	case app.ContactAvailabilityOnline:
		return pb.ContactAvailability_CONTACT_AVAILABILITY_ONLINE
	case app.ContactAvailabilityOffline:
		return pb.ContactAvailability_CONTACT_AVAILABILITY_OFFLINE
	default:
		return pb.ContactAvailability_CONTACT_AVAILABILITY_UNSPECIFIED
	}
}

func toProtoHistorySyncType(t app.HistorySyncType) pb.HistorySyncType {
	switch t {
	case app.HistorySyncTypeInitialBootstrap:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_INITIAL_BOOTSTRAP
	case app.HistorySyncTypeInitialStatusV3:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_INITIAL_STATUS_V3
	case app.HistorySyncTypeFull:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_FULL
	case app.HistorySyncTypeRecent:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_RECENT
	case app.HistorySyncTypePushName:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_PUSH_NAME
	case app.HistorySyncTypeNonBlockingData:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_NON_BLOCKING_DATA
	case app.HistorySyncTypeOnDemand:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_ON_DEMAND
	case app.HistorySyncTypeProfilePicture:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_PROFILE_PICTURE
	case app.HistorySyncTypeOfflineCatchup:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_OFFLINE_CATCHUP
	default:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_UNSPECIFIED
	}
}

func toProtoHistorySyncPhase(phase app.HistorySyncPhase) pb.HistorySyncPhase {
	switch phase {
	case app.HistorySyncPhaseQueued:
		return pb.HistorySyncPhase_HISTORY_SYNC_PHASE_QUEUED
	case app.HistorySyncPhaseDownloading:
		return pb.HistorySyncPhase_HISTORY_SYNC_PHASE_DOWNLOADING
	case app.HistorySyncPhaseProcessing:
		return pb.HistorySyncPhase_HISTORY_SYNC_PHASE_PROCESSING
	case app.HistorySyncPhaseComplete:
		return pb.HistorySyncPhase_HISTORY_SYNC_PHASE_COMPLETE
	default:
		return pb.HistorySyncPhase_HISTORY_SYNC_PHASE_UNSPECIFIED
	}
}

func toProtoChat(chat app.Chat) *pb.Chat {
	return &pb.Chat{
		Id:                   chat.ID,
		Name:                 chat.Name,
		LastMessage:          chat.LastMessage,
		LastMessageTimeUnix:  chat.LastMessageTime,
		UnreadCount:          chat.UnreadCount,
		IsGroup:              chat.IsGroup,
		IsPinned:             chat.IsPinned,
		PinnedOrder:          chat.PinnedOrder,
		IsArchived:           chat.IsArchived,
		IsMuted:              chat.IsMuted,
		MuteEndTimestamp:     chat.MuteEndTimestamp,
		UpdatedAtUnix:        chat.UpdatedAtUnix,
		AvatarLocalPath:      chat.AvatarLocalPath,
		LastMessageDirection: toProtoMessageDirection(chat.LastMessageDirection),
		LastMessageStatus:    toProtoMessageStatus(chat.LastMessageStatus),
	}
}

func toProtoMessage(message app.Message) *pb.Message {
	protoMessage := &pb.Message{
		Id:                      message.ID,
		ChatId:                  message.ChatID,
		SenderId:                message.SenderID,
		SenderName:              message.SenderName,
		SenderAvatarLocalPath:   message.SenderAvatarLocalPath,
		Text:                    message.Text,
		TimestampUnix:           message.TimestampUnix,
		SortSeq:                 message.SortSeq,
		Direction:               toProtoMessageDirection(message.Direction),
		Status:                  toProtoMessageStatus(message.Status),
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
		IsPinned:                message.PinnedUntilUnix > time.Now().Unix(),
		PinnedUntilUnix:         message.PinnedUntilUnix,
	}
	if message.ReplyTo.MessageID != "" {
		protoMessage.ReplyTo = &pb.MessageReply{
			MessageId:     message.ReplyTo.MessageID,
			SenderId:      message.ReplyTo.SenderID,
			SenderName:    message.ReplyTo.SenderName,
			Text:          message.ReplyTo.Text,
			MediaKind:     message.ReplyTo.MediaKind,
			MediaMimeType: message.ReplyTo.MediaMimeType,
			Direction:     toProtoMessageDirection(message.ReplyTo.Direction),
		}
	}
	for _, reaction := range message.Reactions {
		protoMessage.Reactions = append(protoMessage.Reactions, &pb.Reaction{
			Emoji:         reaction.Emoji,
			SenderId:      reaction.SenderID,
			SenderName:    reaction.SenderName,
			TimestampUnix: reaction.TimestampUnix,
			FromMe:        reaction.FromMe,
		})
	}
	return protoMessage
}

func toProtoMessageDirection(direction string) pb.MessageDirection {
	switch direction {
	case "incoming":
		return pb.MessageDirection_MESSAGE_DIRECTION_INCOMING
	case "outgoing":
		return pb.MessageDirection_MESSAGE_DIRECTION_OUTGOING
	default:
		return pb.MessageDirection_MESSAGE_DIRECTION_UNSPECIFIED
	}
}

func toProtoMessageStatus(status string) pb.MessageStatus {
	switch status {
	case "sent":
		return pb.MessageStatus_MESSAGE_STATUS_SENT
	case "delivered":
		return pb.MessageStatus_MESSAGE_STATUS_DELIVERED
	case "read":
		return pb.MessageStatus_MESSAGE_STATUS_READ
	case "failed":
		return pb.MessageStatus_MESSAGE_STATUS_FAILED
	case "pending":
		return pb.MessageStatus_MESSAGE_STATUS_PENDING
	default:
		return pb.MessageStatus_MESSAGE_STATUS_UNSPECIFIED
	}
}

func toProtoState(state app.State) pb.DaemonState {
	switch state {
	case app.StateStarting:
		return pb.DaemonState_DAEMON_STATE_STARTING
	case app.StateNeedLogin:
		return pb.DaemonState_DAEMON_STATE_NEED_LOGIN
	case app.StateConnecting:
		return pb.DaemonState_DAEMON_STATE_CONNECTING
	case app.StateOnline:
		return pb.DaemonState_DAEMON_STATE_ONLINE
	case app.StateReconnecting:
		return pb.DaemonState_DAEMON_STATE_RECONNECTING
	case app.StateOffline:
		return pb.DaemonState_DAEMON_STATE_OFFLINE
	default:
		return pb.DaemonState_DAEMON_STATE_UNSPECIFIED
	}
}
