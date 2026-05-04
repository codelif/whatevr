package notify

import (
	"context"
	"log"
	"net/url"
	"os/exec"
	"sync"

	"github.com/godbus/dbus/v5"

	"whatevrd/internal/app"
)

const (
	busName          = "org.freedesktop.Notifications"
	objectPath       = dbus.ObjectPath("/org/freedesktop/Notifications")
	interfaceName    = "org.freedesktop.Notifications"
	dbusObjectPath   = dbus.ObjectPath("/org/freedesktop/DBus")
	dbusInterface    = "org.freedesktop.DBus"
	defaultTimeoutMS = int32(-1)
)

type Worker struct {
	conn *dbus.Conn
	obj  dbus.BusObject
	caps Capabilities

	mu     sync.Mutex
	active map[uint32]string
	queue  chan queuedMessage
}

type queuedMessage struct {
	message app.Message
	chat    app.Chat
}

func NewWorker() (*Worker, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	w := &Worker{
		conn:   conn,
		obj:    conn.Object(busName, objectPath),
		active: make(map[uint32]string),
		queue:  make(chan queuedMessage, 64),
	}
	w.refreshCapabilities()
	return w, nil
}

func (w *Worker) Start(ctx context.Context) {
	signals := make(chan *dbus.Signal, 16)
	w.conn.Signal(signals)
	_ = w.conn.AddMatchSignal(dbus.WithMatchObjectPath(objectPath), dbus.WithMatchInterface(interfaceName))
	_ = w.conn.AddMatchSignal(dbus.WithMatchObjectPath(dbusObjectPath), dbus.WithMatchInterface(dbusInterface), dbus.WithMatchMember("NameOwnerChanged"))

	go func() {
		defer w.conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case item := <-w.queue:
				w.send(ctx, item.message, item.chat)
			case signal := <-signals:
				w.handleSignal(ctx, signal)
			}
		}
	}()
}

func (w *Worker) NotifyMessage(ctx context.Context, message app.Message, chat app.Chat) {
	select {
	case <-ctx.Done():
	case w.queue <- queuedMessage{message: message, chat: chat}:
	default:
		log.Printf("notification queue full; dropping notification for chat %s", chat.ID)
	}
}

func (w *Worker) refreshCapabilities() {
	var values []string
	if err := w.obj.Call(interfaceName+".GetCapabilities", 0).Store(&values); err != nil {
		log.Printf("notification capabilities unavailable: %v", err)
		w.caps = Capabilities{}
		return
	}
	w.caps = ParseCapabilities(values)
}

func (w *Worker) send(ctx context.Context, message app.Message, chat app.Chat) {
	content := FormatMessage(w.caps, message, chat)
	hints := make(map[string]dbus.Variant, len(content.Hints))
	for key, value := range content.Hints {
		hints[key] = dbus.MakeVariant(value)
	}

	var id uint32
	call := w.obj.CallWithContext(ctx, interfaceName+".Notify", 0,
		"whatevr",
		uint32(0),
		content.Icon,
		content.Summary,
		content.Body,
		content.Actions,
		hints,
		defaultTimeoutMS,
	)
	if err := call.Store(&id); err != nil {
		log.Printf("send notification: %v", err)
		return
	}

	w.mu.Lock()
	w.active[id] = chat.ID
	w.mu.Unlock()
}

func (w *Worker) handleSignal(ctx context.Context, signal *dbus.Signal) {
	if signal == nil {
		return
	}
	switch signal.Name {
	case interfaceName + ".ActionInvoked":
		if len(signal.Body) < 2 {
			return
		}
		id, ok := signal.Body[0].(uint32)
		if !ok {
			return
		}
		chatID, ok := w.activeChat(id)
		if !ok {
			return
		}
		w.openChat(ctx, chatID)
	case interfaceName + ".NotificationClosed":
		if len(signal.Body) < 1 {
			return
		}
		id, ok := signal.Body[0].(uint32)
		if ok {
			w.mu.Lock()
			delete(w.active, id)
			w.mu.Unlock()
		}
	case dbusInterface + ".NameOwnerChanged":
		if len(signal.Body) < 3 {
			return
		}
		name, ok := signal.Body[0].(string)
		if ok && name == busName {
			w.refreshCapabilities()
		}
	}
}

func (w *Worker) activeChat(id uint32) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	chatID, ok := w.active[id]
	return chatID, ok
}

func (w *Worker) openChat(ctx context.Context, chatID string) {
	uri := "whatevr://chat/" + url.PathEscape(chatID)
	if err := exec.CommandContext(ctx, "gio", "open", uri).Start(); err != nil {
		log.Printf("open notification chat %s: %v", chatID, err)
	}
}
