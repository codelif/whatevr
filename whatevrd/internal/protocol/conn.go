package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"sync"
	"time"
)

const (
	// maxLineBytes bounds one NDJSON line; matches the old gRPC message cap.
	maxLineBytes = 16 * 1024 * 1024

	// writeTimeout bounds a single line write so one wedged frontend cannot
	// hold a connection goroutine forever; the outbound queue's coalescing
	// and reset fallback handle merely slow consumers before this bites.
	writeTimeout = 30 * time.Second

	// writeBufferBytes sizes the outbound buffer. Large enough that a typical
	// view fill (an 80-message window is ~47 KB) leaves the write loop in one
	// or two syscalls, small enough to stay negligible per connection.
	writeBufferBytes = 64 * 1024

	// helloTimeout bounds the pre-hello handshake: an accepted peer that never
	// completes hello must not pin a goroutine/fd indefinitely. It is cleared
	// once hello succeeds — post-hello a frontend legitimately idles for as long
	// as it likes listening for pushed events (the protocol has no client
	// keepalive), so there is deliberately no steady-state read timeout.
	helloTimeout = 10 * time.Second
)

type conn struct {
	srv *Server
	nc  net.Conn

	q         *outQueue
	done      chan struct{}
	closeOnce sync.Once

	// ctx is the connection's lifecycle context, cancelled on close. Work that
	// is only useful while this peer can still receive the result (backgrounded
	// queries) derives from it; view sessions keep their own per-session
	// contexts.
	ctx    context.Context
	cancel context.CancelFunc

	// helloDone flips after successful negotiation; every other method is
	// rejected until then.
	helloDone bool

	// The protocol connection itself is the frontend session. It is lazily
	// announced to the daemon when session.update first arrives and ended on
	// close, replacing the legacy HoldSession stream. sessionMu also guards the
	// routing state used by connection-directed open_chat events, which may be
	// emitted from outside this connection's dispatch goroutine.
	sessionMu           sync.Mutex
	sessionID           string
	sessionActive       bool
	sessionFocused      bool
	sessionActiveChatID string
	sessionUpdatedAt    time.Time

	// subMu guards the subscription registry and nextSub. Both are touched
	// from the per-subscribe goroutines as well as the dispatch loop, so id
	// allocation order across concurrent subscribes is unspecified — ids are
	// opaque handles, nothing depends on it.
	subMu      sync.Mutex
	subs       map[int64]*subscription
	nextSub    int64
	subsClosed bool
}

func newConn(srv *Server, nc net.Conn) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &conn{
		srv:    srv,
		nc:     nc,
		q:      newOutQueue(),
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		subs:   map[int64]*subscription{},
	}
}

// run owns the read/dispatch loop; the write loop runs alongside. It
// returns when the peer disconnects, a fatal frame closes the connection,
// or the server shuts down (which closes the socket under us).
func (c *conn) run() {
	defer c.srv.connDone(c)
	defer c.close()

	go c.writeLoop()

	scanner := bufio.NewScanner(c.nc)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	// Arm the handshake deadline; handleHello clears it on success.
	_ = c.nc.SetReadDeadline(time.Now().Add(helloTimeout))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		c.handleLine(line)
	}
	if err := scanner.Err(); err != nil {
		c.reportReadError(err)
	}
}

// reportReadError surfaces why the read loop ended instead of dropping the
// framing failure silently. An oversized frame or malformed stream gets a
// best-effort error response (the peer may already be gone); a handshake
// timeout and any other I/O error are logged. A clean EOF leaves scanner.Err()
// nil and never reaches here.
func (c *conn) reportReadError(err error) {
	var ne net.Error
	switch {
	case errors.Is(err, net.ErrClosed):
		// We closed the socket (shutdown, unsubscribe teardown, or a prior fatal
		// frame). This is normal termination, not a framing failure.
	case errors.Is(err, bufio.ErrTooLong):
		log.Printf("protocol: oversized frame (> %d bytes) closed the connection", maxLineBytes)
		c.respondError(nullID, errorf(CodeInvalidRequest, "frame exceeds the %d byte maximum", maxLineBytes), true)
	case errors.As(err, &ne) && ne.Timeout():
		if !c.helloDone {
			log.Printf("protocol: closing connection: no hello within %s", helloTimeout)
		} else {
			log.Printf("protocol: connection read timed out: %v", err)
		}
	default:
		log.Printf("protocol: connection read error: %v", err)
	}
}

// writeLoop drains the outbound queue through a buffered writer, flushing only
// when the queue runs dry (or the buffer fills). A view fill is a burst of one
// frame per item; writing each one straight to the socket meant a syscall per
// item and, on the frontend side, a separate readyRead — so an 80-message chat
// window arrived as 80 wakeups, each applied to the GUI thread on its own. One
// flush per burst delivers it as one or two reads instead, which is what lets
// the frontend coalesce the fill into a single model transaction.
func (c *conn) writeLoop() {
	w := bufio.NewWriterSize(c.nc, writeBufferBytes)
	for {
		frame, ok := c.q.pop()
		if !ok {
			// Queue empty: everything buffered is the whole burst, so push it
			// out before parking. Nothing may sit in the buffer across a wait —
			// the peer would stall waiting for a frame we are holding.
			if w.Buffered() > 0 {
				_ = c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
				if err := w.Flush(); err != nil {
					c.close()
					return
				}
			}
			select {
			case <-c.q.signal:
				continue
			case <-c.done:
				return
			}
		}
		_ = c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := w.Write(frame.line); err != nil {
			c.close()
			return
		}
		if err := w.WriteByte('\n'); err != nil {
			c.close()
			return
		}
		if frame.closeAfter {
			// The terminal frame must reach the peer before the socket goes.
			_ = w.Flush()
			c.close()
			return
		}
	}
}

// close is idempotent and unblocks both loops: closing the socket ends the
// scanner, closing done ends the writer. It also releases every view
// session, which is the connection-drop half of subscription cleanup.
func (c *conn) close() {
	c.closeOnce.Do(func() {
		c.cancel()
		close(c.done)
		_ = c.nc.Close()

		c.subMu.Lock()
		subs := make([]*subscription, 0, len(c.subs))
		for _, sub := range c.subs {
			subs = append(subs, sub)
		}
		c.subs = map[int64]*subscription{}
		c.subsClosed = true
		c.subMu.Unlock()
		for _, sub := range subs {
			sub.close()
		}
		c.sessionMu.Lock()
		active, id := c.sessionActive, c.sessionID
		c.sessionActive = false
		c.sessionMu.Unlock()
		if active && c.srv.commandActions != nil {
			c.srv.commandActions.FrontendSessionEnded(id)
		}
	})
}

// sendEvent implements frameSink over the outbound queue.
func (c *conn) sendEvent(sub int64, itemKey string, line []byte) (reset bool) {
	return c.q.pushEvent(sub, itemKey, line)
}

func (c *conn) nextSubID() int64 {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	c.nextSub++
	return c.nextSub
}

// registerSub admits a subscription; it reports false when the connection
// already closed (close() emptied the registry), in which case the caller owns
// releasing the session — nothing else holds a reference to it.
func (c *conn) registerSub(sub *subscription) bool {
	c.subMu.Lock()
	if c.subsClosed {
		c.subMu.Unlock()
		return false
	}
	c.subs[sub.id] = sub
	c.subMu.Unlock()
	c.q.addSub(sub.id)
	return true
}

func (c *conn) subscription(id int64) (*subscription, bool) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	sub, ok := c.subs[id]
	return sub, ok
}

func (c *conn) takeSubscription(id int64) (*subscription, bool) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	sub, ok := c.subs[id]
	if ok {
		delete(c.subs, id)
	}
	return sub, ok
}

func (c *conn) handleLine(line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		c.respondError(nullID, errorf(CodeInvalidRequest, "request is not a JSON object"), false)
		return
	}
	if !validRequestID(req.ID) {
		c.respondError(nullID, errorf(CodeInvalidRequest, "request id must be a number or string"), false)
		return
	}
	if req.Method == "" {
		c.respondError(req.ID, errorf(CodeInvalidRequest, "request has no method"), false)
		return
	}

	if req.Method == "hello" {
		c.handleHello(req)
		return
	}
	if !c.helloDone {
		c.respondError(req.ID, errorf(CodeInvalidRequest, "the first request on a connection must be hello"), false)
		return
	}

	handler, ok := c.srv.handler(req.Method)
	if !ok {
		c.respondError(req.ID, errorf(CodeUnknownMethod, "unknown method %q", req.Method), false)
		return
	}
	result, herr := handler(c, req)
	if herr != nil {
		c.respondError(req.ID, herr, false)
		return
	}
	if _, ok := result.(responded); ok {
		return
	}
	if result == nil {
		result = map[string]any{}
	}
	c.respondResult(req.ID, result)
}

type helloParams struct {
	Client   string `json:"client"`
	Protocol *int   `json:"protocol"`
}

func (c *conn) handleHello(req request) {
	if c.helloDone {
		c.respondError(req.ID, errorf(CodeInvalidRequest, "hello already completed on this connection"), false)
		return
	}

	var params helloParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.respondError(req.ID, errorf(CodeInvalidParams, "malformed hello params"), true)
			return
		}
	}
	// Rejecting the connection (closeAfter) is PROTOCOL.md's "the daemon
	// rejects the connection if it cannot speak the requested protocol".
	if params.Protocol == nil {
		c.respondError(req.ID, errorf(CodeInvalidParams, "hello params must carry an integer protocol version"), true)
		return
	}
	if *params.Protocol != ProtocolVersion {
		c.respondError(req.ID, errorf(CodeInvalidParams, "unsupported protocol version %d (daemon speaks %d)", *params.Protocol, ProtocolVersion), true)
		return
	}

	c.helloDone = true
	// Handshake complete: drop the read deadline. A subscribed frontend may now
	// sit silent indefinitely waiting for pushed events.
	_ = c.nc.SetReadDeadline(time.Time{})
	status := c.srv.daemon.Status()
	c.respondResult(req.ID, map[string]any{
		"daemon":    "whatevrd",
		"version":   Version,
		"protocol":  ProtocolVersion,
		"state":     stateString(status.State),
		"data_dir":  status.Paths.DataDir,
		"cache_dir": status.Paths.CacheDir,
	})
}

func (c *conn) respondResult(id json.RawMessage, result any) {
	c.send(response{ID: id, Result: result}, false)
}

func (c *conn) respondError(id json.RawMessage, e *Error, closeAfter bool) {
	c.send(response{ID: id, Error: e}, closeAfter)
}

func (c *conn) send(resp response, closeAfter bool) {
	line, err := json.Marshal(resp)
	if err != nil {
		// A result we produced failed to marshal; that is a daemon bug, and
		// the request still must get its one response.
		log.Printf("protocol: marshal response: %v", err)
		line, _ = json.Marshal(response{ID: resp.ID, Error: errorf(CodeInternal, "failed to encode response")})
	}
	c.q.push(line, closeAfter)
}
