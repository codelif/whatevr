package protocol

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"whatevrd/internal/app"
)

// fakeMessageInfo is a mutable GetMessageInfo backend: tests set the info a
// message id resolves to (and can mark it gone), standing in for *wa.Client's
// store-backed derivation.
type fakeMessageInfo struct {
	mu   sync.Mutex
	byID map[string]app.MessageInfo
	gone map[string]bool
}

func newFakeMessageInfo() *fakeMessageInfo {
	return &fakeMessageInfo{byID: map[string]app.MessageInfo{}, gone: map[string]bool{}}
}

func (f *fakeMessageInfo) GetMessageInfo(_ context.Context, id string) (app.MessageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone[id] {
		return app.MessageInfo{}, sql.ErrNoRows
	}
	info, ok := f.byID[id]
	if !ok {
		return app.MessageInfo{}, sql.ErrNoRows
	}
	return info, nil
}

func (f *fakeMessageInfo) set(id string, info app.MessageInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[id] = info
}

func (f *fakeMessageInfo) delete(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gone[id] = true
}

// receiptTimes pulls delivered/read/played out of a receipts upsert item.
func receiptTimes(t *testing.T, msg map[string]any) (delivered, read, played float64) {
	t.Helper()
	item, ok := msg["item"].(map[string]any)
	if !ok {
		t.Fatalf("receipts upsert without an item: %v", msg)
	}
	d, _ := item["delivered_ts_unix"].(float64)
	r, _ := item["read_ts_unix"].(float64)
	p, _ := item["played_ts_unix"].(float64)
	return d, r, p
}

// A group message: the initial fill is one item per member (including a member
// with no receipt yet), and a later per-member receipt — the kind that does not
// advance the message's aggregate status — arrives as that member's upsert.
func TestReceiptsGroupInitialAndLiveReceipt(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeMessageInfo()
	server.RegisterView("receipts", receiptsView{daemon: server.daemon, actions: actions})

	actions.set("m-group", app.MessageInfo{
		IsGroup: true,
		Receipts: []app.ParticipantReceipt{
			{JID: "aaa@s.whatsapp.net", DisplayName: "Alice", DeliveredTsUnix: 1000, ReadTsUnix: 1000},
			{JID: "bbb@s.whatsapp.net", DisplayName: "Bob"},
		},
	})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"receipts","message_id":"m-group"}`)

	// Fill in sort (jid) order: Alice read, Bob nothing yet.
	alice := c.expectUpsert(sub, "aaa@s.whatsapp.net")
	if d, r, _ := receiptTimes(t, alice); d != 1000 || r != 1000 {
		t.Fatalf("alice delivered/read = %v/%v", d, r)
	}
	bob := c.expectUpsert(sub, "bbb@s.whatsapp.net")
	if d, r, _ := receiptTimes(t, bob); d != 0 || r != 0 {
		t.Fatalf("bob should have no receipt yet, got %v/%v", d, r)
	}
	c.expectReady(sub, true)

	// Bob reads: a per-member receipt that need not move the aggregate tick.
	// The DaemonEventMessageReceipt must still re-derive the view.
	actions.set("m-group", app.MessageInfo{
		IsGroup: true,
		Receipts: []app.ParticipantReceipt{
			{JID: "aaa@s.whatsapp.net", DisplayName: "Alice", DeliveredTsUnix: 1000, ReadTsUnix: 1000},
			{JID: "bbb@s.whatsapp.net", DisplayName: "Bob", DeliveredTsUnix: 2000, ReadTsUnix: 2000},
		},
	})
	server.daemon.PublishMessageReceipt("group@g.us", "m-group")

	bob = c.expectUpsert(sub, "bbb@s.whatsapp.net")
	if d, r, _ := receiptTimes(t, bob); d != 2000 || r != 2000 {
		t.Fatalf("bob after read = %v/%v", d, r)
	}
}

// A direct message: nothing until delivery begins, then the single recipient
// item under the sentinel id, updating from delivered to read.
func TestReceiptsDirectDeliveryThenRead(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeMessageInfo()
	server.RegisterView("receipts", receiptsView{daemon: server.daemon, actions: actions})

	actions.set("m-direct", app.MessageInfo{IsGroup: false}) // sent, not yet delivered

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"receipts","message_id":"m-direct"}`)
	c.expectReady(sub, true) // no delivery yet: empty view

	actions.set("m-direct", app.MessageInfo{IsGroup: false, DeliveredTsUnix: 3000})
	server.daemon.PublishMessageReceipt("aditi@s.whatsapp.net", "m-direct")
	msg := c.expectUpsert(sub, "peer")
	if d, r, _ := receiptTimes(t, msg); d != 3000 || r != 0 {
		t.Fatalf("peer delivered/read = %v/%v", d, r)
	}

	actions.set("m-direct", app.MessageInfo{IsGroup: false, DeliveredTsUnix: 3000, ReadTsUnix: 3100})
	server.daemon.PublishMessageReceipt("aditi@s.whatsapp.net", "m-direct")
	msg = c.expectUpsert(sub, "peer")
	if d, r, _ := receiptTimes(t, msg); d != 3000 || r != 3100 {
		t.Fatalf("peer after read = %v/%v", d, r)
	}
}

// Deleting the message empties the view (the info dialog's message is gone).
func TestReceiptsDeletedEmptiesView(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeMessageInfo()
	server.RegisterView("receipts", receiptsView{daemon: server.daemon, actions: actions})

	actions.set("m-del", app.MessageInfo{IsGroup: false, DeliveredTsUnix: 4000})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"receipts","message_id":"m-del"}`)
	c.expectUpsert(sub, "peer")
	c.expectReady(sub, true)

	actions.delete("m-del")
	server.daemon.PublishMessageDeleted("aditi@s.whatsapp.net", "m-del", app.Chat{})
	c.expectRemove(sub, "peer")
}

// A receipt event for a different message must not touch this subscription.
func TestReceiptsScopedToMessage(t *testing.T) {
	socketPath, server := startTestServer(t)
	actions := newFakeMessageInfo()
	server.RegisterView("receipts", receiptsView{daemon: server.daemon, actions: actions})

	actions.set("m-mine", app.MessageInfo{IsGroup: false})

	c := dialTest(t, socketPath)
	c.hello()
	sub := c.subscribe(2, `{"view":"receipts","message_id":"m-mine"}`)
	c.expectReady(sub, true)

	// Another message's receipt, then this message's delivery: the first event
	// the client sees must be the peer upsert, proving the other was filtered.
	server.daemon.PublishMessageReceipt("x@s.whatsapp.net", "m-other")
	actions.set("m-mine", app.MessageInfo{IsGroup: false, DeliveredTsUnix: 5000})
	server.daemon.PublishMessageReceipt("mine@s.whatsapp.net", "m-mine")
	c.expectUpsert(sub, "peer")
}

func TestReceiptsNotFound(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("receipts", receiptsView{daemon: server.daemon, actions: newFakeMessageInfo()})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"receipts","message_id":"nope"}}`)
	if code := errorCode(t, c.recv()); code != CodeNotFound {
		t.Fatalf("unknown message error code = %q, want %q", code, CodeNotFound)
	}
}

func TestReceiptsRequiresMessageID(t *testing.T) {
	socketPath, server := startTestServer(t)
	server.RegisterView("receipts", receiptsView{daemon: server.daemon, actions: newFakeMessageInfo()})

	c := dialTest(t, socketPath)
	c.hello()
	c.sendLine(`{"id":2,"method":"subscribe","params":{"view":"receipts"}}`)
	if code := errorCode(t, c.recv()); code != CodeInvalidParams {
		t.Fatalf("missing message_id error code = %q, want %q", code, CodeInvalidParams)
	}
}
