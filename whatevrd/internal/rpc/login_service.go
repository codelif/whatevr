package rpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

type LoginController interface {
	Logout(context.Context) error
}

type LoginService struct {
	pb.UnimplementedLoginServiceServer
	daemon *app.Daemon
	login  LoginController
}

func NewLoginService(daemon *app.Daemon, login LoginController) *LoginService {
	return &LoginService{daemon: daemon, login: login}
}

func (s *LoginService) SubscribeLoginEvents(_ *pb.SubscribeLoginEventsRequest, stream pb.LoginService_SubscribeLoginEventsServer) error {
	events, cancel := s.daemon.SubscribeLoginEvents()
	defer cancel()

	for {
		select {
		case event := <-events:
			if err := stream.Send(toProtoLoginEvent(event)); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return nil
		}
	}
}

func (s *LoginService) Logout(ctx context.Context, _ *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	if s.login == nil {
		return nil, status.Error(codes.Unimplemented, "login controller is not available")
	}

	if err := s.login.Logout(ctx); err != nil {
		return nil, err
	}

	return &pb.LogoutResponse{}, nil
}

func toProtoLoginEvent(event app.LoginEvent) *pb.LoginEvent {
	switch event.Kind {
	case app.LoginEventQR:
		return &pb.LoginEvent{
			Payload: &pb.LoginEvent_QrCode{
				QrCode: &pb.QrCode{
					Code:          event.QRCode,
					ExpiresAtUnix: event.ExpiresAt.Unix(),
				},
			},
		}
	default:
		return &pb.LoginEvent{
			Payload: &pb.LoginEvent_LoginStateChanged{
				LoginStateChanged: &pb.LoginStateChanged{
					State:  toProtoState(event.State),
					Detail: event.Detail,
				},
			},
		}
	}
}
