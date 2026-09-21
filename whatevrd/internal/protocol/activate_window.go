package protocol

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// activateWindowEvent is the connection-directed event that asks a frontend
// to raise its window (e.g. from a tray-icon click). It carries no payload:
// the frontend just un-hides and raises.
type activateWindowEvent struct {
	Event string `json:"event"`
}

// ShowTrayMenuEvent asks a frontend to show a context menu at the given
// screen coordinates (originating from a tray right-click). The coordinates
// may be 0,0 when the platform did not supply them.
type showTrayMenuEvent struct {
	Event string `json:"event"`
	X     int32  `json:"x,omitempty"`
	Y     int32  `json:"y,omitempty"`
}

// ActivateWindow sends an activate_window connection-directed event to the
// most recently updated active frontend session, falling back to the most
// recently updated live session. It returns whether a connected frontend
// received the event. When it returns false the caller should cold-start
// one (e.g. via xdg-open).
func (s *Server) ActivateWindow() bool {
	line, err := json.Marshal(activateWindowEvent{Event: "activate_window"})
	if err != nil {
		return false
	}
	return s.dispatchConnectionDirected(line)
}

// ShowTrayMenu sends a show_tray_menu event to the most recently updated
// active frontend session. Returns whether a frontend received it.
func (s *Server) ShowTrayMenu(x, y int32) bool {
	line, err := json.Marshal(showTrayMenuEvent{Event: "show_tray_menu", X: x, Y: y})
	if err != nil {
		return false
	}
	return s.dispatchConnectionDirected(line)
}

// dispatchConnectionDirected sends a connection-directed event (no `sub`)
// to the most recently used active+focused frontend, falling back to the most
// recently used active frontend. It mirrors OpenChat's targeting logic but
// takes a pre-serialized line. Returns false when no active frontend exists.
func (s *Server) dispatchConnectionDirected(line []byte) bool {
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	var target *conn
	var targetAt time.Time
	for _, c := range conns {
		state := c.routeState()
		if !state.active {
			continue
		}
		if target == nil || state.updatedAt.After(targetAt) {
			target = c
			targetAt = state.updatedAt
		}
	}
	if target == nil {
		return false
	}
	target.enqueueOpenChat(line)
	return true
}

// ColdStartApp launches the frontend when no running instance can be
// reached. It tries xdg-open with the whatevr:// URL scheme, so the desktop
// entry's Exec line decides how to actually raise or start the window.
func ColdStartApp() {
	if err := exec.Command("xdg-open", "whatevr://").Start(); err != nil {
		fmt.Println("tray: failed to cold-start frontend:", err)
	}
}
