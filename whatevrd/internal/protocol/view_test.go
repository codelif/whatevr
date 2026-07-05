package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memView is the dummy in-memory view the engine is tested against: a map
// of items with daemon-style opaque sort keys, notifying every open
// session on change.
type memView struct {
	mu       sync.Mutex
	items    map[string]Item
	sessions map[*memSession]struct{}
	openErr  *Error
	meta     map[string]any
}

func newMemView() *memView {
	return &memView{
		items:    map[string]Item{},
		sessions: map[*memSession]struct{}{},
	}
}

func (v *memView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.openErr != nil {
		return nil, nil, v.openErr
	}
	s := &memSession{view: v, invalidate: invalidate}
	v.mu.Lock()
	v.sessions[s] = struct{}{}
	v.mu.Unlock()
	return s, v.meta, nil
}

func (v *memView) put(id, sortKey string, fields map[string]any) {
	data := map[string]any{"id": id}
	for k, val := range fields {
		data[k] = val
	}
	v.mu.Lock()
	v.items[id] = Item{ID: id, Sort: sortKey, Data: data}
	v.notifyLocked()
}

func (v *memView) delete(id string) {
	v.mu.Lock()
	delete(v.items, id)
	v.notifyLocked()
}

// notifyLocked snapshots sessions and unlocks before invalidating, per the
// View contract (recomputes call Items, which needs v.mu).
func (v *memView) notifyLocked() {
	sessions := make([]*memSession, 0, len(v.sessions))
	for s := range v.sessions {
		sessions = append(sessions, s)
	}
	v.mu.Unlock()
	for _, s := range sessions {
		s.invalidate()
	}
}

func (v *memView) sessionCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.sessions)
}

type memSession struct {
	view       *memView
	invalidate func()
}

func (s *memSession) Items(max int) []Item {
	s.view.mu.Lock()
	defer s.view.mu.Unlock()
	items := make([]Item, 0, len(s.view.items))
	for _, it := range s.view.items {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Sort != items[j].Sort {
			return items[i].Sort < items[j].Sort
		}
		return items[i].ID < items[j].ID
	})
	if max > 0 && len(items) > max {
		items = items[:max]
	}
	return items
}

func (s *memSession) Close() {
	s.view.mu.Lock()
	defer s.view.mu.Unlock()
	delete(s.view.sessions, s)
}

// startViewServer is startTestServer plus a registered memView named "mem".
func startViewServer(t *testing.T) (string, *memView) {
	t.Helper()
	socketPath, server := startTestServer(t)
	view := newMemView()
	server.RegisterView("mem", view)
	return socketPath, view
}

// event assertion helpers over the raw testClient

func (c *testClient) recvEvent() map[string]any {
	c.t.Helper()
	msg := c.recv()
	if _, ok := msg["event"]; !ok {
		c.t.Fatalf("expected an event, got %v", msg)
	}
	return msg
}

func (c *testClient) expectUpsert(sub float64, id string) map[string]any {
	c.t.Helper()
	msg := c.recvEvent()
	if msg["event"] != "upsert" || msg["sub"] != sub {
		c.t.Fatalf("expected upsert for sub %v, got %v", sub, msg)
	}
	sortKey, ok := msg["sort"].(string)
	if !ok || sortKey == "" {
		c.t.Fatalf("upsert without a sort key: %v", msg)
	}
	item, ok := msg["item"].(map[string]any)
	if !ok {
		c.t.Fatalf("upsert without an item: %v", msg)
	}
	if item["id"] != id {
		c.t.Fatalf("upsert item id = %v, want %v (%v)", item["id"], id, msg)
	}
	return msg
}

func (c *testClient) expectRemove(sub float64, id string) {
	c.t.Helper()
	msg := c.recvEvent()
	if msg["event"] != "remove" || msg["sub"] != sub || msg["id"] != id {
		c.t.Fatalf("expected remove of %q on sub %v, got %v", id, sub, msg)
	}
}

func (c *testClient) expectReady(sub float64, exhausted bool) {
	c.t.Helper()
	msg := c.recvEvent()
	if msg["event"] != "ready" || msg["sub"] != sub {
		c.t.Fatalf("expected ready on sub %v, got %v", sub, msg)
	}
	if got, _ := msg["exhausted"].(bool); got != exhausted {
		c.t.Fatalf("ready exhausted = %v, want %v (%v)", got, exhausted, msg)
	}
}

// subscribe issues a subscribe and returns the sub id from the response,
// without touching the events that follow.
func (c *testClient) subscribe(reqID int, params string) float64 {
	c.t.Helper()
	c.sendLine(fmt.Sprintf(`{"id":%d,"method":"subscribe","params":%s}`, reqID, params))
	msg := c.recv()
	result, ok := msg["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("subscribe failed: %v", msg)
	}
	sub, ok := result["sub"].(float64)
	if !ok {
		c.t.Fatalf("subscribe result has no sub: %v", msg)
	}
	return sub
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSubscribeFillThenReady(t *testing.T) {
	socketPath, view := startViewServer(t)
	view.put("a", "1", map[string]any{"body": "first"})
	view.put("b", "2", map[string]any{"body": "second"})
	view.put("c", "3", map[string]any{"body": "third"})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"mem"}`)

	// Response already read by subscribe(); the fill follows in view
	// order, then ready. The full view fits, so ready says exhausted.
	prev := ""
	for _, id := range []string{"a", "b", "c"} {
		msg := c.expectUpsert(sub, id)
		if sortKey := msg["sort"].(string); sortKey <= prev {
			t.Fatalf("fill not in ascending sort order: %q after %q", sortKey, prev)
		} else {
			prev = sortKey
		}
	}
	c.expectReady(sub, true)
}

func TestSubscribeErrors(t *testing.T) {
	socketPath, _ := startViewServer(t)
	c := dialTest(t, socketPath)
	c.hello()

	for _, tc := range []struct{ params, code string }{
		{`{"view":"nope"}`, CodeNotFound},
		{`{}`, CodeInvalidParams},
		{`{"view":"mem","limit":0}`, CodeInvalidParams},
		{`{"view":"mem","limit":-2}`, CodeInvalidParams},
		{`{"view":"mem","limit":"ten"}`, CodeInvalidParams},
	} {
		c.sendLine(`{"id":5,"method":"subscribe","params":` + tc.params + `}`)
		if code := errorCode(t, c.recv()); code != tc.code {
			t.Errorf("subscribe %s: error code = %q, want %q", tc.params, code, tc.code)
		}
	}
}

func TestSubscribeOpenErrorLeaksNoSession(t *testing.T) {
	socketPath, server := startTestServer(t)
	view := newMemView()
	view.openErr = errorf(CodeInvalidParams, "bad view params")
	server.RegisterView("mem", view)

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"mem"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("error code = %q, want %q", code, CodeInvalidParams)
	}
	if n := view.sessionCount(); n != 0 {
		t.Fatalf("failed subscribe left %d sessions open", n)
	}
}

func TestSubscribeMetaMergedIntoResult(t *testing.T) {
	socketPath, server := startTestServer(t)
	view := newMemView()
	view.meta = map[string]any{"anchor_id": "m-17", "sub": "must-not-clobber"}
	server.RegisterView("mem", view)

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"mem"}}`)
	result, ok := c.recv()["result"].(map[string]any)
	if !ok {
		t.Fatalf("subscribe failed")
	}
	if result["anchor_id"] != "m-17" {
		t.Fatalf("view meta not merged into subscribe result: %v", result)
	}
	if _, isNumber := result["sub"].(float64); !isNumber {
		t.Fatalf("view meta clobbered the sub id: %v", result)
	}
}

func TestLiveUpsertChangeAndRemove(t *testing.T) {
	socketPath, view := startViewServer(t)
	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"mem"}`)
	c.expectReady(sub, true)

	view.put("a", "1", map[string]any{"body": "hi"})
	msg := c.expectUpsert(sub, "a")
	if body := msg["item"].(map[string]any)["body"]; body != "hi" {
		t.Fatalf("item body = %v, want hi", body)
	}

	// Content change, same position: upsert with the same sort.
	view.put("a", "1", map[string]any{"body": "edited"})
	msg = c.expectUpsert(sub, "a")
	if body := msg["item"].(map[string]any)["body"]; body != "edited" {
		t.Fatalf("item body = %v, want edited", body)
	}

	// Move: same item, new sort key.
	view.put("a", "9", map[string]any{"body": "edited"})
	if msg = c.expectUpsert(sub, "a"); msg["sort"] != "9" {
		t.Fatalf("moved item sort = %v, want 9", msg["sort"])
	}

	view.delete("a")
	c.expectRemove(sub, "a")
}

func TestUnchangedItemNotResent(t *testing.T) {
	socketPath, view := startViewServer(t)
	view.put("a", "1", map[string]any{"body": "same"})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"mem"}`)
	c.expectUpsert(sub, "a")
	c.expectReady(sub, true)

	// An identical rewrite must produce no traffic; the next event the
	// client sees is the genuinely new item.
	view.put("a", "1", map[string]any{"body": "same"})
	view.put("z", "2", map[string]any{"body": "new"})
	c.expectUpsert(sub, "z")
}

func TestWindowLimitAndFallOut(t *testing.T) {
	socketPath, view := startViewServer(t)
	view.put("a", "1", nil)
	view.put("b", "2", nil)
	view.put("c", "3", nil)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"mem","limit":2}`)
	c.expectUpsert(sub, "a")
	c.expectUpsert(sub, "b")
	c.expectReady(sub, false) // c exists beyond the window

	// A new item sorting first enters the window and pushes b out.
	view.put("0", "0", nil)
	got := map[string]string{}
	for range 2 {
		msg := c.recvEvent()
		switch msg["event"] {
		case "upsert":
			got["upsert"] = msg["item"].(map[string]any)["id"].(string)
		case "remove":
			got["remove"] = msg["id"].(string)
		default:
			t.Fatalf("unexpected event %v", msg)
		}
	}
	if got["upsert"] != "0" || got["remove"] != "b" {
		t.Fatalf("window shift sent %v, want upsert 0 + remove b", got)
	}
}

func TestExtendGrowsWindow(t *testing.T) {
	socketPath, view := startViewServer(t)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		view.put(id, fmt.Sprint(i), nil)
	}

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"mem","limit":2}`)
	c.expectUpsert(sub, "a")
	c.expectUpsert(sub, "b")
	c.expectReady(sub, false)

	c.sendLine(fmt.Sprintf(`{"id":3,"method":"extend","params":{"sub":%d,"count":2}}`, int(sub)))
	if msg := c.recv(); msg["error"] != nil || msg["id"] != float64(3) {
		t.Fatalf("extend failed: %v", msg)
	}
	c.expectUpsert(sub, "c")
	c.expectUpsert(sub, "d")
	c.expectReady(sub, false)

	c.sendLine(fmt.Sprintf(`{"id":4,"method":"extend","params":{"sub":%d,"count":10}}`, int(sub)))
	if msg := c.recv(); msg["error"] != nil {
		t.Fatalf("extend failed: %v", msg)
	}
	c.expectUpsert(sub, "e")
	c.expectReady(sub, true)
}

func TestExtendErrors(t *testing.T) {
	socketPath, view := startViewServer(t)
	view.put("a", "1", nil)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"mem","limit":1}`)
	c.expectUpsert(sub, "a")
	c.expectReady(sub, true)

	for _, tc := range []struct{ params, code string }{
		{`{"sub":99,"count":5}`, CodeNotFound},
		{`{"count":5}`, CodeInvalidParams},
		{fmt.Sprintf(`{"sub":%d}`, int(sub)), CodeInvalidParams},
		{fmt.Sprintf(`{"sub":%d,"count":0}`, int(sub)), CodeInvalidParams},
	} {
		c.sendLine(`{"id":9,"method":"extend","params":` + tc.params + `}`)
		if code := errorCode(t, c.recv()); code != tc.code {
			t.Errorf("extend %s: error code = %q, want %q", tc.params, code, tc.code)
		}
	}
}

func TestUnsubscribeStopsEventsAndClosesSession(t *testing.T) {
	socketPath, view := startViewServer(t)
	view.put("a", "1", nil)

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"mem"}`)
	c.expectUpsert(sub, "a")
	c.expectReady(sub, true)

	c.sendLine(fmt.Sprintf(`{"id":3,"method":"unsubscribe","params":{"sub":%d}}`, int(sub)))
	if msg := c.recv(); msg["error"] != nil {
		t.Fatalf("unsubscribe failed: %v", msg)
	}
	waitFor(t, "session close", func() bool { return view.sessionCount() == 0 })

	// Mutations after unsubscribe reach a closed session: no events. The
	// next line on the wire is the probe's response.
	view.put("b", "2", nil)
	c.sendLine(`{"id":4,"method":"no.such.method"}`)
	msg := c.recv()
	if msg["id"] != float64(4) {
		t.Fatalf("expected probe response, got %v", msg)
	}

	// The sub id is gone: further unsubscribes/extends say not_found.
	c.sendLine(fmt.Sprintf(`{"id":5,"method":"unsubscribe","params":{"sub":%d}}`, int(sub)))
	if code := errorCode(t, c.recv()); code != CodeNotFound {
		t.Fatalf("double unsubscribe code = %q, want %q", code, CodeNotFound)
	}
}

func TestTwoSubscriptionsSameView(t *testing.T) {
	socketPath, view := startViewServer(t)
	view.put("a", "1", nil)
	view.put("b", "2", nil)

	c := dialTest(t, socketPath)
	c.hello()
	sub1 := c.subscribe(2, `{"view":"mem","limit":1}`)
	c.expectUpsert(sub1, "a")
	c.expectReady(sub1, false)
	sub2 := c.subscribe(3, `{"view":"mem"}`)
	c.expectUpsert(sub2, "a")
	c.expectUpsert(sub2, "b")
	c.expectReady(sub2, true)
	if sub1 == sub2 {
		t.Fatalf("subscriptions share an id: %v", sub1)
	}

	// An item sorting first hits both subs: the windowed one also drops a.
	view.put("0", "0", nil)
	seen := map[float64][]string{}
	for range 3 {
		msg := c.recvEvent()
		sub := msg["sub"].(float64)
		if msg["event"] == "upsert" {
			seen[sub] = append(seen[sub], "upsert "+msg["item"].(map[string]any)["id"].(string))
		} else {
			seen[sub] = append(seen[sub], msg["event"].(string)+" "+msg["id"].(string))
		}
	}
	if got := strings.Join(seen[sub2], ","); got != "upsert 0" {
		t.Fatalf("unwindowed sub saw %q, want just upsert 0", got)
	}
	gotWindowed := strings.Join(seen[sub1], ",")
	if gotWindowed != "upsert 0,remove a" && gotWindowed != "remove a,upsert 0" {
		t.Fatalf("windowed sub saw %q, want upsert 0 + remove a", gotWindowed)
	}
}

func TestConnectionDropClosesSessions(t *testing.T) {
	socketPath, view := startViewServer(t)

	c := dialTest(t, socketPath)
	c.hello()
	c.subscribe(2, `{"view":"mem"}`)
	if n := view.sessionCount(); n != 1 {
		t.Fatalf("session count = %d, want 1", n)
	}

	c.conn.Close()
	waitFor(t, "session close on connection drop", func() bool { return view.sessionCount() == 0 })
}
