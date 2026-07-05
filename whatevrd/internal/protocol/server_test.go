package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatevrd/internal/app"
)

// startTestServer brings up a protocol server on a private socket and tears
// it down (waiting for full shutdown) when the test ends.
func startTestServer(t *testing.T) (string, *app.Daemon) {
	t.Helper()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod socket dir: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")

	daemon := app.NewDaemon(app.Paths{DataDir: "/data-dir", CacheDir: "/cache-dir"})
	daemon.SetState(app.StateOnline)

	ctx, cancel := context.WithCancel(context.Background())
	server, err := Start(ctx, socketPath, daemon)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		for err := range server.Err() {
			t.Errorf("server error during shutdown: %v", err)
		}
	})

	return socketPath, daemon
}

type testClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialTest(t *testing.T, socketPath string) *testClient {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial %s: %v", socketPath, err)
	}
	t.Cleanup(func() { conn.Close() })
	return &testClient{t: t, conn: conn, r: bufio.NewReader(conn)}
}

// sendLine writes one raw line; the tests speak the wire format by hand on
// purpose, exactly like a socat user would.
func (c *testClient) sendLine(line string) {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		c.t.Fatalf("write %q: %v", line, err)
	}
}

func (c *testClient) recv() map[string]any {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read response: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		c.t.Fatalf("response is not JSON: %v (%q)", err, line)
	}
	return msg
}

// expectClosed asserts the daemon hangs up (EOF or reset) without sending
// anything further.
func (c *testClient) expectClosed() {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if line, err := c.r.ReadBytes('\n'); err == nil {
		c.t.Fatalf("expected connection close, got line %q", line)
	}
}

func errorCode(t *testing.T, msg map[string]any) string {
	t.Helper()
	errObj, ok := msg["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error response, got %v", msg)
	}
	if _, hasResult := msg["result"]; hasResult {
		t.Fatalf("response carries both result and error: %v", msg)
	}
	code, _ := errObj["code"].(string)
	if errObj["message"] == "" {
		t.Fatalf("error has no human-readable message: %v", msg)
	}
	return code
}

func (c *testClient) hello() {
	c.t.Helper()
	c.sendLine(`{"id":1,"method":"hello","params":{"client":"test","protocol":1}}`)
	msg := c.recv()
	if _, ok := msg["result"].(map[string]any); !ok {
		c.t.Fatalf("hello failed: %v", msg)
	}
}

func TestHello(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine(`{"id":7,"method":"hello","params":{"client":"test","protocol":1}}`)
	msg := c.recv()

	if got := msg["id"]; got != float64(7) {
		t.Fatalf("id not echoed: got %v", got)
	}
	if _, hasError := msg["error"]; hasError {
		t.Fatalf("hello failed: %v", msg)
	}
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("hello result missing: %v", msg)
	}
	for field, want := range map[string]any{
		"daemon":    "whatevrd",
		"version":   Version,
		"protocol":  float64(ProtocolVersion),
		"state":     "online",
		"data_dir":  "/data-dir",
		"cache_dir": "/cache-dir",
	} {
		if got := result[field]; got != want {
			t.Errorf("hello result %s = %v, want %v", field, got, want)
		}
	}
}

func TestHelloEchoesStringID(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine(`{"id":"req-a","method":"hello","params":{"protocol":1}}`)
	if got := c.recv()["id"]; got != "req-a" {
		t.Fatalf("string id not echoed verbatim: got %v", got)
	}
}

func TestHelloReflectsDaemonState(t *testing.T) {
	socketPath, daemon := startTestServer(t)
	daemon.SetState(app.StateNeedLogin)
	c := dialTest(t, socketPath)

	c.sendLine(`{"id":1,"method":"hello","params":{"protocol":1}}`)
	result := c.recv()["result"].(map[string]any)
	if got := result["state"]; got != "need_login" {
		t.Fatalf("state = %v, want need_login", got)
	}
}

func TestHelloUnsupportedProtocolRejectsConnection(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine(`{"id":1,"method":"hello","params":{"client":"test","protocol":2}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("error code = %q, want %q", code, CodeInvalidParams)
	}
	c.expectClosed()
}

func TestHelloMissingProtocolRejectsConnection(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine(`{"id":1,"method":"hello","params":{"client":"test"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("error code = %q, want %q", code, CodeInvalidParams)
	}
	c.expectClosed()
}

func TestMethodBeforeHello(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine(`{"id":3,"method":"subscribe","params":{"view":"chats"}}`)
	msg := c.recv()
	if code := errorCode(t, msg); code != CodeInvalidRequest {
		t.Fatalf("error code = %q, want %q", code, CodeInvalidRequest)
	}
	if got := msg["id"]; got != float64(3) {
		t.Fatalf("id not echoed on error: got %v", got)
	}

	// The connection survives and hello still works afterwards.
	c.hello()
}

func TestUnknownMethodAfterHello(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"no.such.method"}`)
	msg := c.recv()
	if code := errorCode(t, msg); code != CodeUnknownMethod {
		t.Fatalf("error code = %q, want %q", code, CodeUnknownMethod)
	}
	if got := msg["id"]; got != float64(2) {
		t.Fatalf("id not echoed: got %v", got)
	}
}

func TestDoubleHello(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":2,"method":"hello","params":{"protocol":1}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidRequest {
		t.Fatalf("error code = %q, want %q", code, CodeInvalidRequest)
	}

	// The connection stays usable after the rejected re-hello.
	c.sendLine(`{"id":3,"method":"no.such.method"}`)
	if code := errorCode(t, c.recv()); code != CodeUnknownMethod {
		t.Fatalf("connection unusable after double hello")
	}
}

func TestMalformedLine(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine(`this is not json`)
	msg := c.recv()
	if code := errorCode(t, msg); code != CodeInvalidRequest {
		t.Fatalf("error code = %q, want %q", code, CodeInvalidRequest)
	}
	if got, present := msg["id"]; !present || got != nil {
		t.Fatalf("garbage line should get a null id, got %v", got)
	}

	// NDJSON framing is intact after a bad line; the connection keeps working.
	c.hello()
}

func TestRequestWithoutValidID(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	for _, line := range []string{
		`{"method":"hello","params":{"protocol":1}}`,
		`{"id":null,"method":"hello","params":{"protocol":1}}`,
		`{"id":{"nested":true},"method":"hello","params":{"protocol":1}}`,
		`{"id":[1],"method":"hello","params":{"protocol":1}}`,
		`{"id":true,"method":"hello","params":{"protocol":1}}`,
	} {
		c.sendLine(line)
		msg := c.recv()
		if code := errorCode(t, msg); code != CodeInvalidRequest {
			t.Fatalf("line %q: error code = %q, want %q", line, code, CodeInvalidRequest)
		}
		if got := msg["id"]; got != nil {
			t.Fatalf("line %q: id should be null, got %v", line, got)
		}
	}
}

func TestRequestWithoutMethod(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine(`{"id":9}`)
	msg := c.recv()
	if code := errorCode(t, msg); code != CodeInvalidRequest {
		t.Fatalf("error code = %q, want %q", code, CodeInvalidRequest)
	}
	if got := msg["id"]; got != float64(9) {
		t.Fatalf("id not echoed: got %v", got)
	}
}

func TestBlankLinesIgnored(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)

	c.sendLine("")
	c.sendLine("   ")
	c.hello()
}

func TestOneResponsePerRequestInOrder(t *testing.T) {
	socketPath, _ := startTestServer(t)
	c := dialTest(t, socketPath)
	c.hello()

	c.sendLine(`{"id":10,"method":"a"}` + "\n" + `{"id":11,"method":"b"}`)
	if got := c.recv()["id"]; got != float64(10) {
		t.Fatalf("first response id = %v, want 10", got)
	}
	if got := c.recv()["id"]; got != float64(11) {
		t.Fatalf("second response id = %v, want 11", got)
	}
}

func TestConnectionsAreIndependentSessions(t *testing.T) {
	socketPath, _ := startTestServer(t)

	a := dialTest(t, socketPath)
	b := dialTest(t, socketPath)
	a.hello()

	// b has not said hello; a's negotiation must not leak onto b's session.
	b.sendLine(`{"id":1,"method":"whoami"}`)
	if code := errorCode(t, b.recv()); code != CodeInvalidRequest {
		t.Fatalf("second connection inherited hello state (code %q)", code)
	}

	a.sendLine(`{"id":2,"method":"whoami"}`)
	if code := errorCode(t, a.recv()); code != CodeUnknownMethod {
		t.Fatalf("first connection lost hello state (code %q)", code)
	}
}

func TestSocketPermissions(t *testing.T) {
	socketPath, _ := startTestServer(t)

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket permissions = %o, want 600", perm)
	}
}

func TestShutdownClosesConnectionsAndRemovesSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod socket dir: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")
	daemon := app.NewDaemon(app.Paths{})

	ctx, cancel := context.WithCancel(context.Background())
	server, err := Start(ctx, socketPath, daemon)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	c := dialTest(t, socketPath)
	c.hello()

	cancel()
	for err := range server.Err() {
		t.Fatalf("server error during shutdown: %v", err)
	}

	c.expectClosed()
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file not removed on shutdown: %v", err)
	}
}
