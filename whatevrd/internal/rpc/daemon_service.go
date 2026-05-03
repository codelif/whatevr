package rpc

import (
	"context"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

const Version = "dev"

type DaemonService struct {
	pb.UnimplementedDaemonServiceServer
	daemon *app.Daemon
}

func NewDaemonService(daemon *app.Daemon) *DaemonService {
	return &DaemonService{daemon: daemon}
}

func (s *DaemonService) GetStatus(context.Context, *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	status := s.daemon.Status()
	return &pb.GetStatusResponse{
		State:        toProtoState(status.State),
		StateLabel:   status.State.String(),
		SocketPath:   status.Paths.SocketPath,
		DataDir:      status.Paths.DataDir,
		CacheDir:     status.Paths.CacheDir,
		DatabasePath: status.Paths.DatabasePath,
		Version:      Version,
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
					State:  toProtoState(event.State),
					Detail: event.Detail,
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
	default:
		return nil
	}
}

func toProtoChat(chat app.Chat) *pb.Chat {
	return &pb.Chat{
		Id:                  chat.ID,
		Name:                chat.Name,
		LastMessage:         chat.LastMessage,
		LastMessageTimeUnix: chat.LastMessageTime,
		UnreadCount:         chat.UnreadCount,
		IsGroup:             chat.IsGroup,
		AvatarLocalPath:     chat.AvatarLocalPath,
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
