package server

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	v2 "github.com/codelif/whatevr/proto/whatevr/v2"
)

// field numbers of the frames assembled by hand below, from frame.proto
const (
	frameEvent      = 3
	eventUpdate     = 1
	updateSub       = 1
	updateReset     = 2
	updateChanges   = 3
	updateReady     = 4
	readyExhausted  = 1
	updatePosition  = 5
	positionCount   = 1
	positionOffset  = 2
	changeUpsert    = 1
	changeRemove    = 2
	removeID        = 1
	removeReplaced  = 2
	maxFrameBytes   = 16 << 20
	frameHeadBudget = 64
)

// a ViewUpdate is put together from upserts marshalled once, when the engine
// hashed them, rather than marshalled again inside a Frame.

// upsertChange is a Change holding the marshalled Upsert u.
func upsertChange(u []byte) []byte {
	b := make([]byte, 0, len(u)+protowire.SizeVarint(uint64(len(u)))+1)
	b = protowire.AppendTag(b, changeUpsert, protowire.BytesType)
	return protowire.AppendBytes(b, u)
}

func removeChange(id, replacedBy string) []byte {
	var r []byte
	r = protowire.AppendTag(r, removeID, protowire.BytesType)
	r = protowire.AppendString(r, id)
	if replacedBy != "" {
		r = protowire.AppendTag(r, removeReplaced, protowire.BytesType)
		r = protowire.AppendString(r, replacedBy)
	}
	b := protowire.AppendTag(nil, changeRemove, protowire.BytesType)
	return protowire.AppendBytes(b, r)
}

// updateFrame is a whole Frame holding one ViewUpdate.
func updateFrame(sub uint64, reset bool, changes [][]byte, ready, exhausted bool, pos *Position) []byte {
	var u []byte
	u = protowire.AppendTag(u, updateSub, protowire.VarintType)
	u = protowire.AppendVarint(u, sub)
	if reset {
		u = protowire.AppendTag(u, updateReset, protowire.VarintType)
		u = protowire.AppendVarint(u, 1)
	}
	for _, c := range changes {
		u = protowire.AppendTag(u, updateChanges, protowire.BytesType)
		u = protowire.AppendBytes(u, c)
	}
	if ready {
		var r []byte
		if exhausted {
			r = protowire.AppendTag(r, readyExhausted, protowire.VarintType)
			r = protowire.AppendVarint(r, 1)
		}
		u = protowire.AppendTag(u, updateReady, protowire.BytesType)
		u = protowire.AppendBytes(u, r)
	}
	if pos != nil {
		var p []byte
		if pos.Count > 0 {
			p = protowire.AppendTag(p, positionCount, protowire.VarintType)
			p = protowire.AppendVarint(p, pos.Count)
		}
		if pos.Offset > 0 {
			p = protowire.AppendTag(p, positionOffset, protowire.VarintType)
			p = protowire.AppendVarint(p, pos.Offset)
		}
		u = protowire.AppendTag(u, updatePosition, protowire.BytesType)
		u = protowire.AppendBytes(u, p)
	}
	e := protowire.AppendTag(nil, eventUpdate, protowire.BytesType)
	e = protowire.AppendBytes(e, u)
	f := protowire.AppendTag(nil, frameEvent, protowire.BytesType)
	return protowire.AppendBytes(f, e)
}

// changeBytes is how much room a change takes inside an update.
func changeBytes(c []byte) int {
	return 1 + protowire.SizeVarint(uint64(len(c))) + len(c)
}

func marshalFrame(f *v2.Frame) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(f)
}

func responseFrame(r *v2.Response) *v2.Frame {
	f := &v2.Frame{}
	f.SetResponse(r)
	return f
}

func eventFrame(e *v2.Event) *v2.Frame {
	f := &v2.Frame{}
	f.SetEvent(e)
	return f
}

func errorResponse(id uint64, code v2.ErrorCode, msg string) *v2.Response {
	r := &v2.Response{}
	r.SetId(id)
	r.SetError(v2.Error_builder{Code: code, Message: msg}.Build())
	return r
}

func doneResponse(id uint64) *v2.Response {
	r := &v2.Response{}
	r.SetId(id)
	r.SetDone(&v2.Done{})
	return r
}
