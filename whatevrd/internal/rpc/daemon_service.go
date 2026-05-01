package rpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

func (s *DaemonService) SubscribeEvents(*pb.SubscribeEventsRequest, pb.DaemonService_SubscribeEventsServer) error {
	return status.Error(codes.Unimplemented, "event streaming is planned after status plumbing")
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
