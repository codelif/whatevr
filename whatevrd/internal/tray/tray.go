package tray

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"

	"whatevrd/internal/app"
	"whatevrd/internal/protocol"
	"whatevrd/internal/store"
)

const (
	watcherService = "org.kde.StatusNotifierWatcher"
	watcherPath    = "/StatusNotifierWatcher"
	watcherIface   = "org.kde.StatusNotifierWatcher"

	itemIface  = "org.kde.StatusNotifierItem"
	propsIface = "org.freedesktop.DBus.Properties"
)

// WindowActivator is the daemon-side seam the tray uses to reach a connected
// frontend: the protocol Server satisfies it. The tray never holds a window
// itself (it is headless), so activation and menus are forwarded to the most
// recently used live frontend; when none is connected the tray cold-starts one
// via xdg-open so a click is never silently dropped.
type WindowActivator interface {
	ActivateWindow() bool
	ShowTrayMenu(x, y int32) bool
}

// Start registers a StatusNotifierItem for the daemon and keeps its
// state/tooltip in sync with the connection state and unread total. It is
// strictly best-effort: no session bus, no watcher, or any D-Bus error only
// logs. Clicking the item raises the frontend window (or cold-starts one);
// right-clicking asks the frontend to show a context menu.
func Start(ctx context.Context, daemon *app.Daemon, db *store.DB, activator WindowActivator) {
	conn, err := dbus.SessionBus()
	if err != nil {
		log.Printf("tray disabled: no session bus: %v", err)
		return
	}
	item := &statusItem{
		conn:      conn,
		daemon:    daemon,
		db:        db,
		activator: activator,
	}
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	if call := conn.BusObject().Call("org.freedesktop.DBus.RequestName", 0, name, uint32(0)); call.Err != nil {
		log.Printf("tray disabled: cannot own bus name: %v", call.Err)
		return
	}
	if err := conn.Export(item, "/StatusNotifierItem", itemIface); err != nil {
		log.Printf("tray disabled: cannot export item: %v", err)
		return
	}
	if err := conn.Export(item, "/StatusNotifierItem", propsIface); err != nil {
		log.Printf("tray disabled: cannot export properties: %v", err)
		return
	}
	watcher := conn.Object(watcherService, watcherPath)
	if call := watcher.Call(watcherIface+".RegisterStatusNotifierItem", 0, name); call.Err != nil {
		log.Printf("tray: no status-notifier watcher (icon may not show): %v", call.Err)
	}
	item.refresh()
	events, cancel := daemon.SubscribeDaemonEvents()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-events:
			switch evt.Kind {
			case app.DaemonEventConnectionChanged,
				app.DaemonEventChatUpdated,
				app.DaemonEventMessageDeleted,
				app.DaemonEventResync:
				item.refresh()
			}
		}
	}
}

type statusItem struct {
	conn      *dbus.Conn
	daemon    *app.Daemon
	db        *store.DB
	activator WindowActivator

	mu     sync.Mutex
	status string
	title  string
}

// Activate forwards a left-click to the frontend: if a frontend is connected
// it receives an activate_window event, otherwise xdg-open launches one.
func (s *statusItem) Activate(x, y int32) *dbus.Error {
	if s.activator != nil && s.activator.ActivateWindow() {
		return nil
	}
	protocol.ColdStartApp()
	return nil
}

func (s *statusItem) SecondaryActivate(x, y int32) *dbus.Error { return nil }
func (s *statusItem) Scroll(delta int32, orientation string) *dbus.Error {
	return nil
}

// ContextMenu forwards a right-click to the frontend so it can show its
// tray menu. If no frontend is connected, cold-start one so the menu appears.
func (s *statusItem) ContextMenu(x, y int32) *dbus.Error {
	if s.activator != nil && s.activator.ShowTrayMenu(x, y) {
		return nil
	}
	// A right-click with no connected frontend cannot render the in-app menu;
	// cold-starting is the only useful fallback and the new frontend will show
	// the complete menu as soon as it connects.
	protocol.ColdStartApp()
	return nil
}

// Get implements org.freedesktop.DBus.Properties.Get for the item interface.
func (s *statusItem) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	props, err := s.GetAll(iface)
	if err != nil {
		return dbus.Variant{}, err
	}
	value, ok := props[property]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{"no such property"})
	}
	return value, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll.
func (s *statusItem) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != itemIface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{"no such interface"})
	}
	s.mu.Lock()
	status, title := s.status, s.title
	s.mu.Unlock()
	return map[string]dbus.Variant{
		"Category":   dbus.MakeVariant("ApplicationStatus"),
		"Id":         dbus.MakeVariant("whatevr"),
		"Title":      dbus.MakeVariant(title),
		"Status":     dbus.MakeVariant(status),
		"WindowId":   dbus.MakeVariant(int32(0)),
		"IconName":   dbus.MakeVariant("in.codelif.Whatevr"),
		"ItemIsMenu": dbus.MakeVariant(true),
		"ToolTip":    dbus.MakeVariant([]any{"in.codelif.Whatevr", title, ""}),
	}, nil
}

// Set implements org.freedesktop.DBus.Properties.Set (read-only here).
func (s *statusItem) Set(iface, property string, value dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []any{"read-only"})
}

// refresh recomputes state + tooltip and emits PropertiesChanged when either
// moved. Unread totals come straight from the store; the query is one SUM.
func (s *statusItem) refresh() {
	state, _, _, _, _ := s.daemon.ConnectionSnapshot()
	unread := 0
	if s.db != nil {
		if total, err := s.db.TotalUnreadCount(context.Background()); err == nil {
			unread = total
		}
	}
	status := "Passive"
	title := "Whatevr"
	switch state {
	case app.StateOnline:
		// Always Active, never NeedsAttention: the attention pulse reads as a
		// stuck/broken icon, and the unread count in the tooltip already
		// carries the information.
		status = "Active"
		title = "Whatevr — online"
	case app.StateConnecting, app.StateReconnecting, app.StateStarting:
		status = "Active"
		title = "Whatevr — connecting"
	case app.StateNeedLogin:
		status = "Active"
		title = "Whatevr — login required"
	default:
		title = "Whatevr — offline"
	}
	if unread > 0 {
		// Stays Active: the NeedsAttention pulse reads as a stuck/broken
		// icon. The unread count in the tooltip carries the information.
		if unread == 1 {
			title += ", 1 unread message"
		} else {
			title += fmt.Sprintf(", %d unread messages", unread)
		}
	}

	s.mu.Lock()
	changed := status != s.status || title != s.title
	s.status, s.title = status, title
	s.mu.Unlock()
	if !changed {
		return
	}
	_ = s.conn.Emit("/StatusNotifierItem", propsIface+".PropertiesChanged",
		itemIface,
		map[string]dbus.Variant{
			"Status":  dbus.MakeVariant(status),
			"Title":   dbus.MakeVariant(title),
			"ToolTip": dbus.MakeVariant([]any{"in.codelif.Whatevr", title, ""}),
		},
		[]string{},
	)
}
