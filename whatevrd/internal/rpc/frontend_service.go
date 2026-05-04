package rpc

import (
	"context"
	"fmt"
	"time"

	"whatevrd/internal/rpc/pb"
)

type FrontendSessionController interface {
	FrontendSessionStarted(string)
	FrontendSessionEnded(string)
	FrontendSessionStateChanged(string, bool, string)
}

type FrontendService struct {
	pb.UnimplementedFrontendServiceServer
	sessions FrontendSessionController
}

func NewFrontendService(sessions FrontendSessionController) *FrontendService {
	return &FrontendService{sessions: sessions}
}

func (s *FrontendService) HoldSession(req *pb.HoldSessionRequest, stream pb.FrontendService_HoldSessionServer) error {
	sessionID := req.GetSessionId()
	if sessionID == "" {
		sessionID = fmt.Sprintf("frontend-%d", time.Now().UnixNano())
	}

	if s.sessions != nil {
		s.sessions.FrontendSessionStarted(sessionID)
		defer s.sessions.FrontendSessionEnded(sessionID)
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

func (s *FrontendService) UpdateSessionState(_ context.Context, req *pb.UpdateSessionStateRequest) (*pb.UpdateSessionStateResponse, error) {
	if s.sessions != nil && req.GetSessionId() != "" {
		s.sessions.FrontendSessionStateChanged(req.GetSessionId(), req.GetFocused(), req.GetActiveChatId())
	}
	return &pb.UpdateSessionStateResponse{}, nil
}
