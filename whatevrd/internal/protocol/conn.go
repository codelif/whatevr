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
	// hold a connection goroutine forever. A2's outbound queue layers
	// coalescing and reset recovery on top of this.
	writeTimeout = 30 * time.Second

	// outboundBuffer is the per-connection queue of marshaled lines between
	// dispatch and the write loop.
	outboundBuffer = 256
)

// outFrame is one marshaled line queued for the write loop. closeAfter
// tears the connection down once the line is flushed (used when hello
// negotiation rejects the connection).
type outFrame struct {
	line       []byte
	closeAfter bool
}

type conn struct {
	srv *Server
	nc  net.Conn

	out       chan outFrame
	done      chan struct{}
	closeOnce sync.Once

	// helloDone flips after successful negotiation; every other method is
	// rejected until then.
	helloDone bool
}

func newConn(srv *Server, nc net.Conn) *conn {
	return &conn{
		srv:  srv,
		nc:   nc,
		out:  make(chan outFrame, outboundBuffer),
		done: make(chan struct{}),
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
		select {
		case frame := <-c.out:
			_ = c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := c.nc.Write(frame.line); err != nil {
				c.close()
				return
			}
			if frame.closeAfter {
				c.close()
				return
			}
		case <-c.done:
			return
		}
	}
}

// close is idempotent and unblocks both loops: closing the socket ends the
// scanner, closing done ends the writer.
func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.nc.Close()
	})
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

	handler, ok := c.srv.handlers[req.Method]
	if !ok {
		c.respondError(req.ID, errorf(CodeUnknownMethod, "unknown method %q", req.Method), false)
		return
	}
	result, herr := handler(c, req.Params)
	if herr != nil {
		c.respondError(req.ID, herr, false)
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
	frame := outFrame{line: append(line, '\n'), closeAfter: closeAfter}
	select {
	case c.out <- frame:
	case <-c.done:
	}
}
