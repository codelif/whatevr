package protocol

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"whatevrd/internal/app"
)

// maxConnections caps concurrent accepted connections. The socket is same-UID
// only, so this is a safety valve against a runaway local frontend spawning
// unbounded connections, not an adversarial-load control. Generous enough that
// every real frontend a user runs fits well under it.
const maxConnections = 256

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
	// ownsSocket is false when the listener was inherited from systemd, which
	// then owns the socket file and reuses it for the next start; the daemon
	// must not unlink it on shutdown.
	ownsSocket     bool
	errCh          chan error
	commandActions CommandActions

	handlerMu sync.RWMutex
	handlers  map[string]handlerFunc

	viewMu sync.RWMutex
	views  map[string]View

	serveOnce sync.Once

	mu    sync.Mutex
	conns map[*conn]struct{}
	wg    sync.WaitGroup
}

// New binds the whatevr protocol socket on socketPath but does not yet accept
// connections. Callers register all views and commands, then call Serve — so a
// client can never reach the handlers/views maps before they are populated
// (which would both race the maps and answer valid methods with
// unknown_method).
//
// When activated is non-nil (systemd socket activation) that inherited listener
// is used and systemd owns, secures and outlives the socket file; otherwise the
// daemon creates and owns the socket itself.
func New(socketPath string, activated net.Listener, daemon *app.Daemon) (*Server, error) {
	listener := activated
	ownsSocket := false
	if listener != nil {
		// systemd owns the socket file and reuses it for the next start, so
		// closing our end must never unlink it. net.FileListener already leaves
		// unlink off, but say so explicitly: an adopted listener that unlinks on
		// shutdown would leave systemd accepting on a path no client can reach.
		if unixLn, ok := listener.(*net.UnixListener); ok {
			unixLn.SetUnlinkOnClose(false)
		}
	}
	if listener == nil {
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

	server := &Server{
		daemon:     daemon,
		listener:   listener,
		socketPath: socketPath,
		ownsSocket: ownsSocket,
		errCh:      make(chan error, 1),
		views:      map[string]View{},
		conns:      map[*conn]struct{}{},
	}
	server.handlers = map[string]handlerFunc{
		"subscribe":   server.handleSubscribe,
		"extend":      server.handleExtend,
		"unsubscribe": server.handleUnsubscribe,
	}
	return server, nil
}

// Serve begins accepting connections. It must be called after every view and
// command is registered; it is safe to call once. The context governs
// shutdown: cancelling it closes the listener, drains connections, and removes
// the socket, after which Err's channel closes.
func (s *Server) Serve(ctx context.Context) {
	s.serveOnce.Do(func() {
		go s.serve(ctx)
	})
}

// Start binds and immediately serves, registering nothing. It exists for tests
// and simple callers that register on the returned server before dialing it;
// production wiring uses New → register → Serve so registration provably
// precedes the first accepted connection. The handler and view maps are
// mutex-guarded regardless, so late registration is race-free (it only risks a
// transient unknown_method for a client that raced the registration).
func Start(ctx context.Context, socketPath string, daemon *app.Daemon) (*Server, error) {
	server, err := New(socketPath, nil, daemon)
	if err != nil {
		return nil, err
	}
	server.Serve(ctx)
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

	// Snapshot the connections under the lock, then close them outside it:
	// c.close() invokes the daemon's FrontendSessionEnded callback, which must
	// never run while s.mu is held (it can reenter the server, e.g. OpenChat).
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
	s.wg.Wait()

	s.listener.Close()
	if s.ownsSocket {
		_ = os.Remove(s.socketPath)
	}
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
		atCap := len(s.conns) >= maxConnections
		if !atCap {
			s.conns[c] = struct{}{}
		}
		s.mu.Unlock()
		if atCap {
			log.Printf("protocol: refusing connection: at connection cap (%d)", maxConnections)
			_ = nc.Close()
			continue
		}
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
