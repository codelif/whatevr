package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
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
)

type conn struct {
	srv *Server
	nc  net.Conn

	q         *outQueue
	done      chan struct{}
	closeOnce sync.Once

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

	// subMu guards the subscription registry; nextSub only moves on the
	// dispatch goroutine.
	subMu   sync.Mutex
	subs    map[int64]*subscription
	nextSub int64
}

func newConn(srv *Server, nc net.Conn) *conn {
	return &conn{
		srv:  srv,
		nc:   nc,
		q:    newOutQueue(),
		done: make(chan struct{}),
		subs: map[int64]*subscription{},
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
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		c.handleLine(line)
	}
}

func (c *conn) writeLoop() {
	for {
		frame, ok := c.q.pop()
		if !ok {
			select {
			case <-c.q.signal:
				continue
			case <-c.done:
				return
			}
		}
		_ = c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := c.nc.Write(append(frame.line, '\n')); err != nil {
			c.close()
			return
		}
		if frame.closeAfter {
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
		close(c.done)
		_ = c.nc.Close()

		c.subMu.Lock()
		subs := make([]*subscription, 0, len(c.subs))
		for _, sub := range c.subs {
			subs = append(subs, sub)
		}
		c.subs = map[int64]*subscription{}
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

func (c *conn) registerSub(sub *subscription) {
	c.subMu.Lock()
	c.subs[sub.id] = sub
	c.subMu.Unlock()
	c.q.addSub(sub.id)
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
