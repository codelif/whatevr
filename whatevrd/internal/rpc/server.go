package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"google.golang.org/grpc"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

const (
	maxRPCMessageBytes      = 16 * 1024 * 1024
	maxConcurrentRPCStreams = 32
)

type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
	socketPath string
	errCh      chan error
}

func Start(ctx context.Context, socketPath string, daemon *app.Daemon, login LoginController, frontend FrontendSessionController, chatStore ChatStore, chatActions ChatActionController, sendController SendController, reconnector ReconnectController) (*Server, error) {
	if err := validateSocketDir(socketPath); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(socketPath); err != nil {
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

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxRPCMessageBytes),
		grpc.MaxSendMsgSize(maxRPCMessageBytes),
		grpc.MaxConcurrentStreams(maxConcurrentRPCStreams),
	)
	pb.RegisterDaemonServiceServer(grpcServer, NewDaemonService(daemon, reconnector))
	pb.RegisterLoginServiceServer(grpcServer, NewLoginService(daemon, login))
	pb.RegisterFrontendServiceServer(grpcServer, NewFrontendService(frontend))
	pb.RegisterChatServiceServer(grpcServer, NewChatService(daemon, chatStore, chatActions))
	pb.RegisterSendServiceServer(grpcServer, NewSendService(sendController))

	server := &Server{
		grpcServer: grpcServer,
		listener:   sameUIDListener{Listener: listener},
		socketPath: socketPath,
		errCh:      make(chan error, 1),
	}

	go server.serve(ctx)
	return server, nil
}

type sameUIDListener struct {
	net.Listener
}

func (l sameUIDListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := validateSameUIDPeer(conn); err != nil {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}

func validateSocketDir(socketPath string) error {
	parent := filepath.Dir(socketPath)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect socket directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("socket parent is not a directory: %s", parent)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("socket directory must not be accessible by group/other: %s", parent)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect socket directory owner: unsupported file stat")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("socket directory owner does not match current user: %s", parent)
	}
	return nil
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect existing socket: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove symlink at socket path: %s", socketPath)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket at socket path: %s", socketPath)
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

func validateSameUIDPeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("unexpected non-unix rpc peer")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("inspect rpc peer: %w", err)
	}

	var peerCred *syscall.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		peerCred, controlErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("inspect rpc peer credentials: %w", err)
	}
	if controlErr != nil {
		return fmt.Errorf("inspect rpc peer credentials: %w", controlErr)
	}
	if peerCred == nil || peerCred.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("rpc peer uid is not allowed")
	}
	return nil
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
