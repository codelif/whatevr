package rpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

const Version = "dev"

type ReconnectController interface {
	Reconnect(context.Context) error
}

type DaemonService struct {
	pb.UnimplementedDaemonServiceServer
	daemon      *app.Daemon
	reconnector ReconnectController
}

func NewDaemonService(daemon *app.Daemon, reconnector ReconnectController) *DaemonService {
	return &DaemonService{daemon: daemon, reconnector: reconnector}
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
	case app.DaemonEventChatPresence:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_ChatPresenceChanged{
				ChatPresenceChanged: &pb.ChatPresenceChanged{
					ChatId:      event.Chat.ID,
					SenderId:    event.SenderID,
					IsComposing: event.IsComposing,
				},
			},
		}
	case app.DaemonEventHistorySyncProgress:
		return &pb.DaemonEvent{
			Payload: &pb.DaemonEvent_HistorySyncProgress{
				HistorySyncProgress: &pb.HistorySyncProgress{
					SyncType:             toProtoHistorySyncType(event.HistorySync.SyncType),
					ProgressPercent:      event.HistorySync.ProgressPercent,
					ChunkOrder:           event.HistorySync.ChunkOrder,
					ConversationsInChunk: event.HistorySync.ConversationsInChunk,
					MessagesInChunk:      event.HistorySync.MessagesInChunk,
					IsComplete:           event.HistorySync.IsComplete,
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
	default:
		return nil
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
	default:
		return pb.HistorySyncType_HISTORY_SYNC_TYPE_UNSPECIFIED
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
		AvatarLocalPath:      chat.AvatarLocalPath,
		LastMessageDirection: toProtoMessageDirection(chat.LastMessageDirection),
		LastMessageStatus:    toProtoMessageStatus(chat.LastMessageStatus),
	}
}

func toProtoMessage(message app.Message) *pb.Message {
	return &pb.Message{
		Id:             message.ID,
		ChatId:         message.ChatID,
		SenderId:       message.SenderID,
		Text:           message.Text,
		TimestampUnix:  message.TimestampUnix,
		Direction:      toProtoMessageDirection(message.Direction),
		Status:         toProtoMessageStatus(message.Status),
		MediaMimeType:  message.MediaMimeType,
		MediaLocalPath: message.MediaLocalPath,
		MediaWidth:     message.MediaWidth,
		MediaHeight:    message.MediaHeight,
	}
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
