// Package server serves PROTOCOL.md: protocol 2, protobuf frames on a local
// socket. views read the model and the daemon's live state; commands go to
// the whatsapp client.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/reflect/protoreflect"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"

	"whatevrd/internal/core"
)

// Protocol is the version this server speaks.
const Protocol = 2

// a runaway local frontend opening sockets in a loop, not an attacker: the
// socket is the user's own
const maxConnections = 256

type Options struct {
	// Listener is a socket systemd handed over, nil to bind SocketPath
	Listener   net.Listener
	SocketPath string
	Version    string
	DataDir    string
	CacheDir   string
	Log        zerolog.Logger
	// Tap sees every frame in and out, and opens and closes, for captures
	Tap func(conn uint64, dir string, frame []byte)
}

// Method serves one request arm. a nil response is done.
type Method func(ctx context.Context, s *Session, req *v2.Request) (*v2.Response, error)

type method struct {
	fn Method
	// inline runs on the connection's own loop, so two of them from one
	// frontend happen in the order sent: sends, local writes
	inline bool
}

type Server struct {
	opts    Options
	ln      net.Listener
	owns    bool
	views   map[protoreflect.FieldNumber]view
	methods map[protoreflect.FieldNumber]method

	mu    sync.Mutex
	conns map[*conn]struct{}

	// pending is a chat a click or link opened with no frontend to show it
	pendingMu sync.Mutex
	pending   string
	pendingAt time.Time
	wg        sync.WaitGroup
	last      atomic.Uint64

	// OnSessions hears that a frontend came, went, focused, or changed what
	// it shows. set before Serve
	OnSessions func()
	// Fit cuts an item down to limit bytes marshalled, false when it can't.
	// set before Serve
	Fit  func(it *v2.Upsert, limit int) bool
	errc chan error
}

func New(opts Options) (*Server, error) {
	s := &Server{opts: opts, views: map[protoreflect.FieldNumber]view{}, methods: map[protoreflect.FieldNumber]method{},
		conns: map[*conn]struct{}{}, errc: make(chan error, 1)}
	if opts.Listener != nil {
		s.ln = opts.Listener
		// systemd or launchd owns the file and reuses it for the next start
		if u, ok := s.ln.(*net.UnixListener); ok {
			u.SetUnlinkOnClose(false)
		}
		return s, nil
	}
	ln, err := listen(opts.SocketPath)
	if err != nil {
		return nil, err
	}
	s.ln, s.owns = ln, true
	return s, nil
}

type view struct {
	View
	// itemBytes is the most one marshalled item may take, cap the most items
	// a window holds
	itemBytes, cap int
}

// windowBytes is what a whole window may take: a fill or a reset is one
// frame, with room for the frame around it.
const windowBytes = 15 << 20

// WindowCap is the most items a window of items up to itemBytes holds: what
// fits in windowBytes, down to a power of two.
func WindowCap(itemBytes int) int {
	n := 1
	for n*2*itemBytes <= windowBytes {
		n *= 2
	}
	return n
}

// View serves the subscribe arm with field number n, whose items never take
// more than itemBytes marshalled.
func (s *Server) View(n protoreflect.FieldNumber, v View, itemBytes int) {
	s.views[n] = view{View: v, itemBytes: itemBytes, cap: WindowCap(itemBytes)}
}

// Handle serves the request arm with field number n off the connection's
// loop, for anything that waits on the network.
func (s *Server) Handle(n protoreflect.FieldNumber, fn Method) { s.methods[n] = method{fn: fn} }

// HandleInline serves n on the connection's loop, in the order requests came.
func (s *Server) HandleInline(n protoreflect.FieldNumber, fn Method) {
	s.methods[n] = method{fn: fn, inline: true}
}

// features is every view and method served, by its arm name.
func (s *Server) features() []string {
	var out []string
	for n := range s.views {
		out = append(out, viewName(n))
	}
	for n := range s.methods {
		out = append(out, methodName(n))
	}
	out = append(out, "subscribe", "extend", "unsubscribe", "positions")
	sort.Strings(out)
	return out
}

var (
	methodArms = (&v2.Request{}).ProtoReflect().Descriptor().Oneofs().ByName("method").Fields()
	viewArms   = (&v2.Subscribe{}).ProtoReflect().Descriptor().Oneofs().ByName("view").Fields()
)

func methodName(n protoreflect.FieldNumber) string {
	if f := methodArms.ByNumber(n); f != nil {
		return string(f.Name())
	}
	return fmt.Sprint(n)
}

func viewName(n protoreflect.FieldNumber) string {
	if f := viewArms.ByNumber(n); f != nil {
		return string(f.Name())
	}
	return fmt.Sprint(n)
}

// Serve accepts until ctx ends, then closes every connection and the
// socket. Err's channel closes once that is done.
func (s *Server) Serve(ctx context.Context) {
	go func() {
		defer close(s.errc)
		accepted := make(chan error, 1)
		go s.accept(ctx, accepted)
		select {
		case err := <-accepted:
			if err != nil && ctx.Err() == nil {
				s.errc <- err
			}
		case <-ctx.Done():
			s.ln.Close()
			<-accepted
		}
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
		s.ln.Close()
		if s.owns {
			_ = os.Remove(s.opts.SocketPath)
		}
	}()
}

func (s *Server) Err() <-chan error { return s.errc }

func (s *Server) accept(ctx context.Context, done chan<- error) {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				err = nil
			}
			done <- err
			return
		}
		if err := samePeer(nc); err != nil {
			s.opts.Log.Warn().Err(err).Msg("refusing a connection from another user")
			nc.Close()
			continue
		}
		id := s.last.Add(1)
		log := s.opts.Log.With().Uint64("conn", id).Logger()
		cctx, stop := context.WithCancel(log.WithContext(context.Background()))
		c := &conn{srv: s, nc: nc, id: id, log: log, q: newQueue(), done: make(chan struct{}), ctx: cctx, stop: stop,
			subs: map[uint64]*subscription{}, inflight: make(chan struct{}, maxInFlight)}
		c.sess = &Session{conn: c}
		s.mu.Lock()
		full := len(s.conns) >= maxConnections
		if !full {
			s.conns[c] = struct{}{}
		}
		s.mu.Unlock()
		if full {
			log.Warn().Msg("refusing a connection: too many open")
			nc.Close()
			continue
		}
		log.Info().Msg("connection opened")
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

func (s *Server) connections() []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, c)
	}
	return out
}

// Changed wakes every open window c may have moved.
func (s *Server) Changed(c core.Change) {
	for _, cn := range s.connections() {
		for _, sub := range cn.windows() {
			if sub.win.Wake(c) {
				sub.kick()
			}
		}
	}
}

func (s *Server) sessionChanged() {
	if s.OnSessions != nil {
		s.OnSessions()
	}
}

// Session is one connected frontend, as session_update describes it.
type Session struct {
	conn *conn

	mu        sync.Mutex
	client    string
	frontend  string
	focused   bool
	active    string
	notifies  bool
	touched   time.Time
	focusedAt time.Time
}

// Update is session_update.
func (s *Session) Update(focused bool, active string, notifies bool) {
	s.mu.Lock()
	now := time.Now()
	if focused && !s.focused {
		s.focusedAt = now
	}
	s.focused, s.active, s.notifies, s.touched = focused, active, notifies, now
	s.mu.Unlock()
	s.conn.srv.sessionChanged()
}

// SessionState is a frontend as the daemon's own decisions see it.
type SessionState struct {
	// Frontend is the id it said hello with, empty for tools
	Frontend string
	Focused  bool
	// Active is the chat id it has open
	Active    string
	Notifies  bool
	Touched   time.Time
	FocusedAt time.Time
	// Windows is how many views it has open
	Windows int
	// Shown is every view's params, for demand driven work
	Shown []*v2.Subscribe
}

func (s *Session) state() SessionState {
	s.mu.Lock()
	st := SessionState{Frontend: s.frontend, Focused: s.focused, Active: s.active, Notifies: s.notifies, Touched: s.touched, FocusedAt: s.focusedAt}
	s.mu.Unlock()
	for _, w := range s.conn.windows() {
		st.Windows++
		st.Shown = append(st.Shown, w.params)
	}
	return st
}

// Sessions is every frontend that said hello.
func (s *Server) Sessions() []SessionState {
	var out []SessionState
	for _, c := range s.connections() {
		if !c.hello.Load() {
			continue
		}
		out = append(out, c.sess.state())
	}
	return out
}

// pendingFor is how long a chat nobody could open waits for a frontend.
const pendingFor = 60 * time.Second

// target is the frontend most recently focused, or the most recently active
// one if none is focused. only connections that said which frontend they are
// count: a tool on the socket can't show a chat.
func (s *Server) target() *conn {
	var best *conn
	var bestAt time.Time
	focused := false
	for _, c := range s.connections() {
		if !c.hello.Load() {
			continue
		}
		st := c.sess.state()
		if st.Frontend == "" {
			continue
		}
		at := st.Touched
		if st.Focused {
			at = st.FocusedAt
		}
		switch {
		case st.Focused && !focused:
			best, bestAt, focused = c, at, true
		case st.Focused == focused && (best == nil || at.After(bestAt)):
			best, bestAt = c, at
		}
	}
	return best
}

func openChatEvent(chatID string) *v2.Event {
	e := &v2.Event{}
	e.SetOpenChat(v2.OpenChat_builder{ChatId: chatID}.Build())
	return e
}

// OpenChat sends open_chat for chatID to the target frontend. with none it
// holds the chat for the next frontend to say hello, newest wins, and says
// false.
func (s *Server) OpenChat(chatID string) bool {
	if best := s.target(); best != nil {
		best.event(openChatEvent(chatID))
		return true
	}
	s.pendingMu.Lock()
	s.pending, s.pendingAt = chatID, time.Now()
	s.pendingMu.Unlock()
	return false
}

// Activate sends activate to the target frontend, false when there is none.
func (s *Server) Activate() bool {
	best := s.target()
	if best == nil {
		return false
	}
	e := &v2.Event{}
	e.SetActivate(&v2.Activate{})
	best.event(e)
	return true
}

// hand gives a frontend that just said hello the chat held for it.
func (s *Server) hand(c *conn) {
	if c.sess.state().Frontend == "" {
		return
	}
	s.pendingMu.Lock()
	chat, at := s.pending, s.pendingAt
	s.pending = ""
	s.pendingMu.Unlock()
	if chat != "" && time.Since(at) < pendingFor {
		c.event(openChatEvent(chat))
	}
}

// Send hands an event to the connection behind s, if it is still open.
func (s *Session) Send(e *v2.Event) { s.conn.event(e) }

// Error is a request's failure as the wire says it.
type Error struct {
	code v2.ErrorCode
	msg  string
}

func (e *Error) Error() string { return e.msg }

// Code is the wire's code for err, INTERNAL for one that isn't an Error.
func Code(err error) v2.ErrorCode { return asError(err).code }

func Errorf(code v2.ErrorCode, format string, args ...any) error {
	return &Error{code: code, msg: fmt.Sprintf(format, args...)}
}

func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{code: v2.ErrorCode_ERROR_CODE_INTERNAL, msg: err.Error()}
}

func listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := checkDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := removeStale(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func removeStale(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("look at the old socket: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("a symlink sits at the socket path %s", path)
	case info.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("something that isn't a socket sits at %s", path)
	}
	return os.Remove(path)
}
