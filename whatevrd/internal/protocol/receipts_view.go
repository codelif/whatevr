package protocol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"sync"

	"whatevrd/internal/app"
)

// MessageInfoActions re-derives a message's receipt timeline. *wa.Client
// implements it; the `receipts` view re-runs GetMessageInfo on every relevant
// event, so the store stays the single source of truth for receipt state.
type MessageInfoActions interface {
	GetMessageInfo(ctx context.Context, messageID string) (app.MessageInfo, error)
}

// directReceiptID is the stable item id for a direct chat's single recipient.
// GetMessageInfo carries no jid/name for the 1:1 case (only aggregate
// delivered/read times), so the sole participant item uses this sentinel.
const directReceiptID = "peer"

// receiptsView is the per-message delivery view for the info dialog: one item
// per participant with delivered/read/played times. It is unwindowed (no
// `limit`) and derives entirely from GetMessageInfo, re-run on each event:
//   - group: one item per member (including members with no receipt yet), keyed
//     by member jid, carrying the member's display name + avatar.
//   - direct: GetMessageInfo exposes only the aggregate delivered/read (no jid),
//     so a single item under directReceiptID appears once delivery begins.
//
// Live updates arrive as DaemonEventMessageReceipt (fired per recorded receipt,
// even when it does not advance the message's aggregate status — the case a
// bare status-updated event would miss); DaemonEventMessageUpdated is also a
// trigger for robustness, and DaemonEventMessageDeleted empties the view.
type receiptsView struct {
	daemon  *app.Daemon
	actions MessageInfoActions
}

type receiptsParams struct {
	MessageID string `json:"message_id"`
}

type receiptItem struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	AvatarPath      string `json:"avatar_path,omitempty"`
	DeliveredTsUnix int64  `json:"delivered_ts_unix,omitempty"`
	ReadTsUnix      int64  `json:"read_ts_unix,omitempty"`
	PlayedTsUnix    int64  `json:"played_ts_unix,omitempty"`
}

func (v receiptsView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	var p receiptsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed receipts params")
		}
	}
	if p.MessageID == "" {
		return nil, nil, errorf(CodeInvalidParams, "receipts params must carry a message_id")
	}
	if v.actions == nil {
		return nil, nil, errorf(CodeInternal, "receipts view unavailable")
	}
	// Validate the message exists up front so a bad message_id is a clean
	// not_found at subscribe (rather than a silently empty view). Done before
	// subscribing to events so the early return leaks no subscription.
	if _, err := v.actions.GetMessageInfo(context.Background(), p.MessageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, errorf(CodeNotFound, "no message %q", p.MessageID)
		}
		return nil, nil, errorf(CodeInternal, "receipts lookup failed: %v", err)
	}

	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &receiptsSession{
		messageID:    p.MessageID,
		actions:      v.actions,
		eventsCancel: cancel,
		done:         make(chan struct{}),
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

// receiptsSession re-derives GetMessageInfo on demand; it holds no receipt state
// of its own (the store is authoritative), only the message id it watches.
type receiptsSession struct {
	messageID    string
	actions      MessageInfoActions
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *receiptsSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.relevant(evt) {
				invalidate()
			}
		}
	}
}

// relevant reports whether an event pertains to the watched message. A resync
// always re-derives (the store is authoritative, so re-reading recovers from a
// dropped receipt/update event).
func (s *receiptsSession) relevant(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync:
		return true
	case app.DaemonEventMessageReceipt, app.DaemonEventMessageUpdated:
		return evt.Message.ID == s.messageID
	case app.DaemonEventMessageDeleted:
		return evt.DeletedMessageID == s.messageID
	case app.DaemonEventAvatarUpdated, app.DaemonEventContactInfoUpdated:
		// A member's avatar/name can refresh while the receipts dialog is open;
		// Items re-derives GetMessageInfo (name+avatar per member), so re-derive and
		// let the engine diff away rows that did not actually change.
		return true
	default:
		return false
	}
}

// Items re-derives the current receipt breakdown. The view is unwindowed, so max
// is ignored; a deleted (or otherwise unreadable) message yields no items, so
// the engine emits removes and the dialog empties.
func (s *receiptsSession) Items(_ int) []Item {
	info, err := s.actions.GetMessageInfo(context.Background(), s.messageID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("protocol: receipts re-derive %s: %v", s.messageID, err)
		}
		return nil
	}

	if info.IsGroup {
		items := make([]Item, 0, len(info.Receipts))
		for _, r := range info.Receipts {
			items = append(items, Item{
				ID:   r.JID,
				Sort: r.JID,
				Data: receiptItem{
					ID:              r.JID,
					Name:            r.DisplayName,
					AvatarPath:      r.AvatarLocalPath,
					DeliveredTsUnix: r.DeliveredTsUnix,
					ReadTsUnix:      r.ReadTsUnix,
					PlayedTsUnix:    r.PlayedTsUnix,
				},
			})
		}
		return items
	}

	// Direct chat: the sole recipient, represented by the aggregate
	// delivered/read. Nothing to show until delivery begins.
	if info.DeliveredTsUnix == 0 && info.ReadTsUnix == 0 {
		return nil
	}
	return []Item{{
		ID:   directReceiptID,
		Sort: objectViewSort,
		Data: receiptItem{
			ID:              directReceiptID,
			DeliveredTsUnix: info.DeliveredTsUnix,
			ReadTsUnix:      info.ReadTsUnix,
		},
	}}
}

func (s *receiptsSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}
