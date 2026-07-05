package protocol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"whatevrd/internal/app"
)

// handlerFunc serves one method: request in, result (or protocol error)
// out. A nil result marshals as {}; returning responded{} means the handler
// already enqueued its own response (subscribe/extend do, to order the
// response ahead of the events it unleashes). Handlers run on the
// connection's dispatch goroutine; responses and any events they cause go
// through the write loop.
type handlerFunc func(c *conn, req request) (any, *Error)

// Server owns the protocol socket and its connections. hello negotiation is
// built in; views and commands register into handlers as migration steps
// land them.
type Server struct {
	daemon     *app.Daemon
	listener   net.Listener
	socketPath string
	errCh      chan error
	handlers   map[string]handlerFunc

	viewMu sync.RWMutex
	views  map[string]View

	mu    sync.Mutex
	conns map[*conn]struct{}
	wg    sync.WaitGroup
}

// Start serves the whatevr protocol on socketPath. The daemon creates and
// owns the socket file; systemd socket activation for this socket arrives
// when packaging flips over at teardown (the activation unit still carries
// the legacy gRPC socket during the migration).
func Start(ctx context.Context, socketPath string, daemon *app.Daemon) (*Server, error) {
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

	server := &Server{
		daemon:     daemon,
		listener:   listener,
		socketPath: socketPath,
		errCh:      make(chan error, 1),
		views:      map[string]View{},
		conns:      map[*conn]struct{}{},
	}
	server.handlers = map[string]handlerFunc{
		"subscribe":   server.handleSubscribe,
		"extend":      server.handleExtend,
		"unsubscribe": server.handleUnsubscribe,
	}

	go server.serve(ctx)
	return server, nil
}

// Err reports a fatal server error; the channel closes once the server has
// fully shut down and released the socket.
func (s *Server) Err() <-chan error {
	return s.errCh
}

func (s *Server) serve(ctx context.Context) {
	defer close(s.errCh)

	acceptErr := make(chan error, 1)
	go s.acceptLoop(acceptErr)

	select {
	case err := <-acceptErr:
		// Accept failed on its own before any shutdown was requested.
		if err != nil && ctx.Err() == nil {
			s.errCh <- err
		}
	case <-ctx.Done():
		s.listener.Close()
		<-acceptErr
	}

	s.mu.Lock()
	for c := range s.conns {
		c.close()
	}
	s.mu.Unlock()
	s.wg.Wait()

	s.listener.Close()
	_ = os.Remove(s.socketPath)
}

func (s *Server) acceptLoop(acceptErr chan<- error) {
	for {
		nc, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				acceptErr <- nil
			} else {
				acceptErr <- err
			}
			return
		}
		if err := validateSameUIDPeer(nc); err != nil {
			_ = nc.Close()
			continue
		}

		c := newConn(s, nc)
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go c.run()
	}
}

func (s *Server) connDone(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	s.wg.Done()
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
		return fmt.Errorf("unexpected non-unix protocol peer")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("inspect protocol peer: %w", err)
	}

	var peerCred *syscall.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		peerCred, controlErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("inspect protocol peer credentials: %w", err)
	}
	if controlErr != nil {
		return fmt.Errorf("inspect protocol peer credentials: %w", controlErr)
	}
	if peerCred == nil || peerCred.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("protocol peer uid is not allowed")
	}
	return nil
}
