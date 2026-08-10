package protocol

import (
	"encoding/json"
	"time"
)

// Item is one item of a view: a stable id, the daemon-computed opaque sort
// key (frontends order by bytewise comparison, ascending), and the value
// delivered as the upsert's `item`. Data must marshal to a JSON object
// whose `id` field equals ID.
type Item struct {
	ID   string
	Sort string
	Data any
}

// View is a named live collection (or single object) that frontends
// subscribe to; migration steps register implementations with
// Server.RegisterView. Open is called once per subscription and receives
// the raw subscribe params (including the engine-level `view` and `limit`,
// which it may ignore). invalidate schedules a window recomputation; the
// session must call it whenever its contents may have changed, and must
// not hold locks that Items needs while doing so. meta, if non-nil, is
// merged into the subscribe result next to `sub` (e.g. `anchor_id`).
type View interface {
	Open(params json.RawMessage, invalidate func()) (sess ViewSession, meta map[string]any, err *Error)
}

// ViewSession is one subscription's handle onto a view.
//
// Items returns the first max items in view order (all items when
// max <= 0). It must be safe to call from any goroutine; a call may race
// with Close, so a closed session must still return harmlessly (an empty
// slice is fine). invalidate callbacks after Close are ignored.
type ViewSession interface {
	Items(max int) []Item
	Close()
}

// DirectionalSession is an optional ViewSession capability for a windowed view
// whose window is not a live-edge prefix but a range that grows independently
// toward older and newer items — the anchored `messages` window. When Open
// returns a session that implements it, the engine hands window ownership to
// the session: `extend` routes (direction, count) to ExtendWindow instead of
// bumping the generic prefix window, Items(0) reports the session's whole
// current window (the engine does no prefix trim), and Exhausted drives the
// ready event. A plain prefix session (chat lists, object views, the `latest`
// messages window) does not implement this, so the engine rejects a `newer`
// extend against it — its newer edge is the live edge, where items arrive
// unsolicited.
type DirectionalSession interface {
	ViewSession
	// ExtendWindow grows the window toward direction ("older"|"newer") by count.
	// The engine has already validated direction is one of the two legal
	// values; a direction with nothing left to reach is a no-op surfaced
	// through Exhausted, never an error.
	ExtendWindow(direction string, count int)
	// Exhausted reports whether the frontier last extended (or, before any
	// extend, both frontiers) has nothing further locally — the value the
	// ready event carries.
	Exhausted() bool
}

// RegisterView makes a view subscribable under its protocol name
// (e.g. "chats"). Safe to call while the server is accepting connections.
func (s *Server) RegisterView(name string, v View) {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	s.views[name] = v
}

func (s *Server) lookupView(name string) (View, bool) {
	s.viewMu.RLock()
	defer s.viewMu.RUnlock()
	v, ok := s.views[name]
	return v, ok
}

type subscribeParams struct {
	View  string `json:"view"`
	Limit *int   `json:"limit"`
}

func (s *Server) handleSubscribe(c *conn, req request) (any, *Error) {
	var p subscribeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, errorf(CodeInvalidParams, "malformed subscribe params")
		}
	}
	if p.View == "" {
		return nil, errorf(CodeInvalidParams, "subscribe params must name a view")
	}
	view, ok := s.lookupView(p.View)
	if !ok {
		return nil, errorf(CodeNotFound, "no view %q", p.View)
	}
	window := 0
	if p.Limit != nil {
		if *p.Limit <= 0 {
			return nil, errorf(CodeInvalidParams, "limit must be a positive integer")
		}
		window = *p.Limit
	}

	// Open runs off the dispatch goroutine. Some views do real work there — a
	// group roster resolves every member — and inline that blocked every later
	// request on the connection, including the `messages` subscribe the user is
	// actually waiting for. The *response* moves with it, which is what keeps
	// the ordering safe: a frontend cannot reference a `sub` it has not been
	// given, so no extend/unsubscribe can overtake a subscribe still opening,
	// and a failed Open is still that request's plain error response.
	go func() {
		start := time.Now()
		sub := newSubscription(c.nextSubID(), c, window)
		sess, meta, verr := view.Open(req.Params, sub.kick)
		logViewOpen(p.View, start)
		if verr != nil {
			c.respondError(req.ID, verr, false)
			return
		}
		sub.sess = sess
		if d, ok := sess.(DirectionalSession); ok {
			sub.dir = d
		}
		// A connection that closed while Open ran has already torn down its
		// registry, so this session would never be closed by anything else.
		if !c.registerSub(sub) {
			sess.Close()
			return
		}

		result := map[string]any{"sub": sub.id}
		for k, v := range meta {
			if k != "sub" {
				result[k] = v
			}
		}
		// Respond before starting delivery: the initial fill runs after the
		// response is queued, giving the wire order response → upserts → ready.
		c.respondResult(req.ID, result)
		sub.start()
	}()
	return responded{}, nil
}

type extendParams struct {
	Sub       *int64 `json:"sub"`
	Count     *int   `json:"count"`
	Direction string `json:"direction"`
}

func (s *Server) handleExtend(c *conn, req request) (any, *Error) {
	var p extendParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, errorf(CodeInvalidParams, "malformed extend params")
		}
	}
	if p.Sub == nil {
		return nil, errorf(CodeInvalidParams, "extend params must carry a sub")
	}
	if p.Count == nil || *p.Count <= 0 {
		return nil, errorf(CodeInvalidParams, "count must be a positive integer")
	}
	if p.Direction != "older" && p.Direction != "newer" {
		return nil, errorf(CodeInvalidParams, "extend params must carry a direction of \"older\" or \"newer\"")
	}
	sub, ok := c.subscription(*p.Sub)
	if !ok {
		return nil, errorf(CodeNotFound, "no subscription %d", *p.Sub)
	}
	// Validate the direction against the window's shape before responding, so
	// an invalid `newer` on a live-edge window becomes the request's error
	// response rather than a result.
	if verr := sub.validateExtend(p.Direction); verr != nil {
		return nil, verr
	}
	// Respond before growing the window so `ready` cannot precede the
	// response (completion is signaled by ready, per PROTOCOL.md).
	c.respondResult(req.ID, map[string]any{})
	sub.applyExtend(p.Direction, *p.Count)
	return responded{}, nil
}

type unsubscribeParams struct {
	Sub *int64 `json:"sub"`
}

func (s *Server) handleUnsubscribe(c *conn, req request) (any, *Error) {
	var p unsubscribeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, errorf(CodeInvalidParams, "malformed unsubscribe params")
		}
	}
	if p.Sub == nil {
		return nil, errorf(CodeInvalidParams, "unsubscribe params must carry a sub")
	}
	sub, ok := c.takeSubscription(*p.Sub)
	if !ok {
		return nil, errorf(CodeNotFound, "no subscription %d", *p.Sub)
	}
	// Purging before the response guarantees no event for this sub reaches
	// the wire after the unsubscribe completes.
	c.q.closeSub(sub.id)
	sub.close()
	return nil, nil
}
