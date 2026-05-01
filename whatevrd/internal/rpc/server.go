package rpc

import (
	"context"
	"errors"
	"net"
	"os"

	"google.golang.org/grpc"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
	socketPath string
	errCh      chan error
}

func Start(ctx context.Context, socketPath string, daemon *app.Daemon, login LoginController) (*Server, error) {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(socketPath, 0o600); err != nil {
		listener.Close()
		return nil, err
	}

	grpcServer := grpc.NewServer()
	pb.RegisterDaemonServiceServer(grpcServer, NewDaemonService(daemon))
	pb.RegisterLoginServiceServer(grpcServer, NewLoginService(daemon, login))

	server := &Server{
		grpcServer: grpcServer,
		listener:   listener,
		socketPath: socketPath,
		errCh:      make(chan error, 1),
	}

	go server.serve(ctx)
	return server, nil
}

func (s *Server) Err() <-chan error {
	return s.errCh
}

func (s *Server) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		s.grpcServer.Stop()
		s.listener.Close()
		_ = os.Remove(s.socketPath)
	}()

	if err := s.grpcServer.Serve(s.listener); err != nil && ctx.Err() == nil {
		s.errCh <- err
	}
	close(s.errCh)
}
