package rpc

import (
	"time"

	"whatevrd/internal/rpc/pb"
)

type FrontendSessionController interface {
	FrontendSessionStarted()
	FrontendSessionEnded()
}

type FrontendService struct {
	pb.UnimplementedFrontendServiceServer
	sessions FrontendSessionController
}

func NewFrontendService(sessions FrontendSessionController) *FrontendService {
	return &FrontendService{sessions: sessions}
}

func (s *FrontendService) HoldSession(req *pb.HoldSessionRequest, stream pb.FrontendService_HoldSessionServer) error {
	if s.sessions != nil {
		s.sessions.FrontendSessionStarted()
		defer s.sessions.FrontendSessionEnded()
	}

	clientName := req.GetClientName()
	if clientName == "" {
		clientName = "frontend"
	}

	if err := stream.Send(&pb.FrontendSessionEvent{Detail: clientName + " connected"}); err != nil {
		return err
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			if err := stream.Send(&pb.FrontendSessionEvent{Detail: "keepalive"}); err != nil {
				return err
			}
		}
	}
}
