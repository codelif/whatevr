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

The current implementation establishes the daemon/GUI foundation, QR login,
daemon-side text message ingestion, and a native chat UI with text sending:

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
- whatevrd tracks active frontend sessions separately from background sync
- whatevrd marks the account online only while a frontend session is attached
- whatevrd forces visible delivery receipts even when the GUI is closed
- whatevrd exposes ChatService for listing chats, reading messages, and marking chats read
- whatevrd exposes SendService for sending text messages through whatsmeow
- whatevrd sends real WhatsApp read receipts when chats are opened
- whatevrd updates outgoing message status from WhatsApp receipts
- whatevrd emits NewMessage and ChatUpdated daemon events
- whatevr opens as a libadwaita app with ID in.codelif.Whatevr
- whatevr renders QR login as a native libadwaita sign-in page
- whatevr shows an adaptive sidebar/conversation layout with empty states and offline banners
- whatevr lists chats, opens the latest 50 local messages, and provides a native multiline composer
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

## Read-Only Chat UI

The GTK frontend now stays native to libadwaita:

```txt
- responsive split navigation on wide layouts
- stacked sidebar/conversation flow on narrow layouts
- QR login, loading, empty, and offline states use AdwStatusPage/AdwBanner
- chat list reads from local SQLite via ChatService.ListChats
- conversation view reads the latest 50 local messages via ChatService.GetMessages
- selecting a chat clears unread count via ChatService.MarkChatRead
- composer remains hidden until sending is implemented
```

## Presence And Read State

`whatevrd` stays connected in the background for sync, but WhatsApp presence is
now tied to real GUI attachment state:

```txt
- when at least one whatevr frontend session is connected, the daemon sends PresenceAvailable
- when no frontend sessions are connected, the daemon sends PresenceUnavailable
- active delivery receipts are forced so senders still see visible delivered ticks even while the GUI is closed
- opening a chat clears local unread state and also sends real WhatsApp MarkRead receipts
```

## Text Sending

The GTK frontend now exposes a multiline composer when a chat is selected and
the daemon is online:

```txt
- Enter sends
- Shift+Enter inserts a newline
- immediate send failures are shown inline near the composer
- outgoing messages are stored locally after successful SendText RPCs
- delivery/read status updates arrive later from daemon events driven by WhatsApp receipts
```

## MVP 1 Scope

MVP 1 will add notification-click-to-open-chat. Media, stickers, reactions,
search, calls, status, channels, CLI, and TUI are out of scope for MVP 1.
