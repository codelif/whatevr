# whatevr

Native WhatsApp client for Linux.

`whatevrd` is the background daemon. `whatevr` is the official GTK frontend.

## Architecture

```txt
Linux user session
`-- systemd --user
    `-- whatevrd
        |-- WhatsApp session/client
        |-- SQLite local store
        |-- sync/reconnect engine
        |-- notification worker
        `-- gRPC server over Unix socket
            $XDG_RUNTIME_DIR/whatevrd/whatevrd.sock

whatevr
`-- Rust GTK4/libadwaita GUI
    |-- QR login screen
    |-- chat list
    |-- message view
    `-- composer
```

## Repository Layout

```txt
whatevr/
|-- whatevrd/   # Go daemon source
|-- whatevr/    # Rust GTK/libadwaita GUI source
`-- packaging/  # systemd and packaging assets
```

## Runtime Paths

```txt
Socket:     $XDG_RUNTIME_DIR/whatevrd/whatevrd.sock
Lock:       $XDG_RUNTIME_DIR/whatevrd/whatevrd.lock
App DB:     $XDG_DATA_HOME/whatevrd/whatevrd.db
Session DB: $XDG_DATA_HOME/whatevrd/session/whatsmeow.db
Session:    $XDG_DATA_HOME/whatevrd/session/
Cache:      $XDG_CACHE_HOME/whatevrd/media/
```

If `XDG_DATA_HOME` or `XDG_CACHE_HOME` is unset, the daemon uses
`~/.local/share/whatevrd` and `~/.cache/whatevrd`.

## Current Status

The current implementation establishes the daemon/GUI foundation, QR login, and
daemon-side text message ingestion:

```txt
- whatevrd starts
- whatevrd creates runtime/data/cache directories
- whatevrd holds a runtime process lock to prevent duplicate daemon instances
- whatevrd initializes SQLite MVP tables
- whatevrd exposes GetStatus over gRPC on a Unix socket
- whatevrd initializes whatsmeow with a persisted SQLite session store
- whatevrd exposes LoginService.SubscribeLoginEvents
- whatevrd emits QR login codes and login state changes
- whatevrd receives WhatsApp text messages from whatsmeow
- whatevrd stores text messages and chat summaries in SQLite
- whatevrd emits NewMessage and ChatUpdated daemon events
- whatevr opens as a libadwaita app with ID in.codelif.Whatevr
- whatevr connects to whatevrd and displays daemon status
- whatevr subscribes to login events and renders QR codes
- packaging/systemd/whatevrd.service provides a user service template
```

## Run Daemon

```sh
cd whatevrd
go run ./cmd/whatevrd
```

## Run GUI

In another terminal inside the user session:

```sh
cd whatevr
cargo run
```

## Install User Service

After building/installing `whatevrd` to `~/.local/bin/whatevrd`:

```sh
mkdir -p ~/.config/systemd/user
cp packaging/systemd/whatevrd.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now whatevrd.service
```

## QR Login

When no WhatsApp session exists, start the daemon and then open the GUI. The GUI
subscribes to `LoginService.SubscribeLoginEvents` and renders the QR code from
the daemon. After pairing succeeds, the whatsmeow session is persisted in
`$XDG_DATA_HOME/whatevrd/session/whatsmeow.db`.

## Message Ingestion

For MVP 1, `whatevrd` stores only text messages from `events.Message`. Plain
conversation text and extended text are supported. Media, stickers, reactions,
quoted replies, and edits are ignored for now.

Stored messages update the `chats` summary row in the same SQLite transaction.
Duplicate WhatsApp message IDs are ignored so reconnects/history replays do not
double-count unread messages. History-sync messages are stored but do not
increment unread counts.

## MVP 1 Scope

MVP 1 will add GUI chat list/message views, text sending, and
notification-click-to-open-chat. Media, stickers, reactions, search, calls,
status, channels, CLI, and TUI are out of scope for MVP 1.
