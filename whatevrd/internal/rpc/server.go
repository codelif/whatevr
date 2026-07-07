package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

const (
	maxRPCMessageBytes = 16 * 1024 * 1024
	// The frontend multiplexes every service (including a handful of long-lived
	// subscription streams) over one HTTP/2 connection, and bursty work like
	// the sticker picker can briefly want many concurrent unary calls. Keep
	// generous headroom so those bursts never starve the channel.
	maxConcurrentRPCStreams = 128
	gracefulStopTimeout     = 2 * time.Second
)

type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
	socketPath string
	ownsSocket bool
	errCh      chan error
	// shutdown is closed at the start of the shutdown sequence so long-lived
	// streaming handlers return promptly and GracefulStop can finish quickly.
	shutdown chan struct{}
}

// Start serves the gRPC API on socketPath. When activated is non-nil (systemd
// socket activation), that inherited listener is used and systemd owns the
// socket file; otherwise the daemon creates and owns the socket itself.
func Start(ctx context.Context, socketPath string, activated net.Listener, daemon *app.Daemon, login LoginController, frontend FrontendSessionController, bus *SessionBus, chatStore ChatStore, chatActions ChatActionController, sendController SendController, stickerController StickerController, reconnector ReconnectController, settingsController SettingsController) (*Server, error) {
	var (
		listener   net.Listener
		ownsSocket bool
	)
	if activated != nil {
		// systemd created, secured (SocketMode=) and owns the socket file.
		listener = activated
	} else {
		if err := validateSocketDir(socketPath); err != nil {
			return nil, err
		}
		if err := removeStaleSocket(socketPath); err != nil {
			return nil, err
		}

		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(socketPath, 0o600); err != nil {
			ln.Close()
			return nil, err
		}
		listener = ln
		ownsSocket = true
	}

	shutdown := make(chan struct{})

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxRPCMessageBytes),
		grpc.MaxSendMsgSize(maxRPCMessageBytes),
		grpc.MaxConcurrentStreams(maxConcurrentRPCStreams),
		grpc.UnaryInterceptor(commandErrorInterceptor),
	)
	pb.RegisterDaemonServiceServer(grpcServer, NewDaemonService(daemon, reconnector, shutdown))
	pb.RegisterLoginServiceServer(grpcServer, NewLoginService(daemon, login, shutdown))
	pb.RegisterFrontendServiceServer(grpcServer, NewFrontendService(frontend, bus, shutdown))
	pb.RegisterChatServiceServer(grpcServer, NewChatService(daemon, chatStore, chatActions))
	pb.RegisterSendServiceServer(grpcServer, NewSendService(sendController))
	pb.RegisterStickerServiceServer(grpcServer, NewStickerService(stickerController))
	pb.RegisterSettingsServiceServer(grpcServer, NewSettingsService(settingsController))

	server := &Server{
		grpcServer: grpcServer,
		listener:   sameUIDListener{Listener: listener},
		socketPath: socketPath,
		ownsSocket: ownsSocket,
		errCh:      make(chan error, 1),
		shutdown:   shutdown,
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
	defer close(s.errCh)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.grpcServer.Serve(s.listener)
	}()

	select {
	case err := <-serveErr:
		// Serve returned on its own (e.g. listener error) before any shutdown
		// was requested. Surface the error and skip the graceful path.
		if err != nil && ctx.Err() == nil {
			s.errCh <- err
		}
		s.cleanup()
		return
	case <-ctx.Done():
	}

	// Shutdown requested. Signal long-lived streams to return so GracefulStop
	// does not block waiting on them, then stop the server.
	close(s.shutdown)

	stopped := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(gracefulStopTimeout):
		s.grpcServer.Stop()
	}

	// Wait for Serve to return before we tear down the listener/socket, so the
	// cleanup is complete before serve() returns and main() exits.
	<-serveErr
	s.cleanup()
}

func (s *Server) cleanup() {
	s.listener.Close()
	// Only remove the socket file if we created it. Under systemd socket
	// activation systemd owns the socket and reuses it for the next start.
	if s.ownsSocket {
		_ = os.Remove(s.socketPath)
	}
}
