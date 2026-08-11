> _This project is not affiliated with WhatsApp or Meta._
# Whatevr

A Linux-first native client for WhatsApp, built as **one daemon and any number
of thin frontends**. `whatevrd` owns the WhatsApp connection (via
[whatsmeow](https://github.com/tulir/whatsmeow)), the login session, the SQLite
message store, the media cache and notifications. Frontends own pixels. They
talk over a documented protocol on a unix socket, and writing one is a weekend
project, not a fork.

## The protocol is the point

[**PROTOCOL.md**](PROTOCOL.md) is the contract: newline-delimited JSON over
`$XDG_RUNTIME_DIR/whatevr/whatevrd.sock`, protocol version 1, stable. Four ideas
carry the whole thing:

- **The daemon owns all state.** A frontend does no sorting, merging, dedup or
  cache invalidation. Ever. It keeps a map of items and renders it.
- **You subscribe to views, not endpoints.** `subscribe` to `chats`, `messages`,
  `typing`, `presence`… and the daemon sends the contents, then keeps your copy
  correct forever with keyed `upsert`/`remove` events. Every item carries a
  daemon-computed `sort` key; ordering is never your problem.
- **Commands only ever return an id.** Send a message and the response is a
  message id. The message itself arrives through the view you already had open,
  the same way one from your phone would. There is no second code path.
- **Every message renders in every frontend.** Each one carries a `fallback`
  string, so a client that has never heard of stickers still shows something
  sensible. Partial frontends are first-class.

The whole surface is 22 views, 30 commands and 4 queries, and it is meant to be
driven by hand:

```console
$ socat - UNIX-CONNECT:"$XDG_RUNTIME_DIR/whatevr/whatevrd.sock"
{"id":1,"method":"hello","params":{"client":"human","protocol":1}}
{"id":1,"result":{"daemon":"whatevrd","version":"0.6.0","protocol":1,"state":"online","data_dir":"…","cache_dir":"…"}}
{"id":2,"method":"subscribe","params":{"view":"chats","limit":2}}
{"id":2,"result":{"sub":1}}
{"sub":1,"event":"upsert","sort":"0-…","item":{"id":"12036…@g.us","name":"family","unread":3,"preview":"📷 Photo","pinned":true}}
{"sub":1,"event":"ready"}
{"id":3,"method":"send.text","params":{"chat_id":"91887…@s.whatsapp.net","text":"oi"}}
{"id":3,"result":{"message_id":"3EB0…"}}
```

### Write a frontend

Two complete ones ship in [`examples/`](examples), and neither imports a client
library because there isn't one to import:

- [`examples/shell-frontend.sh`](examples/shell-frontend.sh): **a working
  WhatsApp client in ~30 lines of shell**, using nothing but `socat` and `jq`.
  It lists your chats, follows a conversation and sends the lines you type.
  That script is the project's acceptance bar: a protocol change that breaks its
  spirit is the wrong change.
- [`examples/frontend.go`](examples/frontend.go): the same thing in fifty lines
  of Go and the standard library.

A TUI, a scriptable CLI (`whatevrctl send/list/watch`), a status-bar applet or a
bridge to something else are all a socket and a JSON parser away. If a frontend
ever feels awkward to write, that is a bug in the daemon or in PROTOCOL.md.
Report it as one.

## Frontends

### WhatKevr
![whatkevr2](https://github.com/user-attachments/assets/be7e52a0-491c-4f96-972c-b264fa66887b)
![whatkevr](https://github.com/user-attachments/assets/46f96ee9-32a7-4e1d-8cae-1d0e82371f8f)


<details>
    <summary>Other Frontends</summary>
    
### WhatGevr
![whatgevr](https://github.com/user-attachments/assets/785ed14e-77e5-48c2-a7da-ba2f61b1f951)
</details>

## Getting it
On Arch-based systems, Whatevr is available on the AUR:
```sh
yay -S whatevr-bin
```
Note: You can install the `whatevr` or `whatevr-git` packages also if you want to build yourself

For other systems, for now you can follow the build instructions below:

## Building
<details>
    <summary>Build Instructions</summary>
    
whatevr builds through a single top-level `justfile` that compiles **both** the
daemon (`whatevrd`) and the Qt/Kirigami frontend (`whatkevr`). The daemon must be
running for any frontend to work.

#### 1. Install dependencies

**Daemon:** Go 1.25+, just, a C compiler, SQLite dev files, pkg-config.
**Frontend:** C++20 compiler, CMake 3.21+, Ninja, Qt 6.8+, KDE Frameworks 6.5+
(KCoreAddons, KDBusAddons, KI18n, Kirigami, Prison, QQC2 Desktop Style),
Kirigami Addons 1.0+, rlottie, Vulkan headers.

```sh
# Arch
sudo pacman -S --needed base-devel go just sqlite pkgconf cmake ninja \
  extra-cmake-modules vulkan-headers qt6-base qt6-declarative qt6-shadertools \
  kcoreaddons kdbusaddons ki18n kirigami kirigami-addons prison qqc2-desktop-style rlottie 

# Note: rlottie is not available on the official Arch repos, you can install it from the AUR 

# Fedora
sudo dnf install go just gcc gcc-c++ sqlite-devel pkgconf-pkg-config cmake ninja-build \
  extra-cmake-modules vulkan-headers qt6-qtbase-devel qt6-qtdeclarative-devel \
  qt6-qtshadertools-devel kf6-kcoreaddons-devel \
  kf6-kdbusaddons-devel kf6-ki18n-devel kf6-kirigami-devel kf6-prison-devel \
  kf6-qqc2-desktop-style-devel kf6-kirigami-addons-devel rlottie-devel

# Debian 13 "trixie" (needs Go >= 1.25 — see Platform support)
sudo apt install golang just gcc g++ libsqlite3-dev pkg-config cmake ninja-build \
  extra-cmake-modules vulkan-headers qt6-base-dev qt6-declarative-dev qt6-shadertools-dev \
  libkf6coreaddons-dev libkf6dbusaddons-dev libkf6i18n-dev \
  libkf6kirigami-dev libkf6prison-dev libkf6qqc2desktopstyle-dev kirigami-addons-dev librlottie-dev
```

#### 2. Build and install

```sh
just build                            # debug build for local testing
just build-release                    # optimized release build
just install "$HOME/.local"           # user-local release install
# or system-wide:
sudo just install /usr
```

`just install` places the `whatevrd` and `whatkevr` binaries, desktop entry,
icon, AppStream metainfo and the systemd user units under the selected prefix.
Make sure the chosen `bin` directory is on your `PATH` (e.g. `~/.local/bin`).

Other handy targets: `just version`, `just validate`, `just artifacts`, and
`just clean`.

#### 3. Run

Start the daemon, then the frontend:

```sh
whatevrd      # or run it via systemd (below)
whatkevr
```

#### Run the daemon via systemd (optional)

`just install` ships two **mutually exclusive** user units — enable **one**,
never both (they share the same socket path):

- **Socket activation (recommended):** the daemon starts on demand the moment a
  frontend connects, and keeps running afterwards.
- **Always-on service:** the daemon starts at login.

```sh
systemctl --user daemon-reload
systemctl --user enable --now whatevrd.socket     # socket activation (recommended)
# or
systemctl --user enable --now whatevrd.service    # always-on
```

A **user-local** install puts the units under `~/.local/lib/systemd/user`, which
systemd does not search. Copy them into a searched path first (the templated
service needs the binary path substituted):

```sh
mkdir -p ~/.config/systemd/user
sed "s|@BINDIR@|$HOME/.local/bin|g" packaging/systemd/whatevrd.service.in \
  > ~/.config/systemd/user/whatevrd.service
cp packaging/systemd/whatevrd.socket ~/.config/systemd/user/
systemctl --user daemon-reload
```

Distro packages install both units to `/usr/lib/systemd/user/` (shipped disabled).

#### Notification deep links

Clicking a message notification opens that chat directly via the
`whatevr://chat/<id>` URL scheme. Distro packages register the handler
automatically; for a manual install, register it once:

```sh
update-desktop-database ~/.local/share/applications
xdg-mime default in.codelif.Whatevr.desktop x-scheme-handler/whatevr
```

#### Other frontends: whatgevr (unmaintained)

The GTK4/libadwaita frontend is not actively maintained and is excluded from the
main build and packaging. Build it manually if you want to hack on it:

```sh
# deps: rust, gtk4, libadwaita, pkg-config
cd whatgevr
cargo build --release
install -Dm755 target/release/whatevr ~/.local/bin/whatevr
```

</details>


## Status
Whatevr is very early-stage software. It is usable for development and testing, but the should be treated as **EXPERIMENTAL**. 
There is lots of missing functionality that is considered essential, and there WILL be bugs.

The **protocol**, on the other hand, is stable at version 1: new views, commands
and message kinds will be added, but nothing already in PROTOCOL.md changes
shape. A frontend written against it today keeps working.

Now with that, here is the current feature map, this is for whatevrd+whatkevr.
<details>
  <summary>Feature Map</summary>
  
| Feature | Status | Notes |
| --- | --- | --- |
| WhatsApp login with QR code | ✅ | |
| Persistent login session | ✅ | |
| Logout | ✅ | |
| Local message database | ✅ | SQLite |
| Older message loading | ✅ | |
| Incoming messages | ✅ | |
| Send text messages | ✅ | |
| Send image messages | ✅ | |
| Reply to messages | ✅ | |
| Message delivery/read status | ✅ | |
| Pin and unpin chats | ✅ | |
| Group chats | ✅ | Basic support + info display |
| Chat avatars | ✅ | |
| Media preview/display | ✅ | Images and cached media |
| Paste image from clipboard | ✅ | |
| Typing indicator | ✅ | Send/receive composing state |
| Online/last-seen presence | ✅ | |
| Offline/history sync progress | ✅ | |
| Desktop notifications | ✅ | Handled by daemon |
| Emoji picker | ✅ | Frontend-local |
| Message search | ✅ | |
| Chat search | ✅ | |
| Contact search/new chat | ✅ | |
| Voice messages | ❌ | |
| Audio playback | ❌ | |
| Video playback | ❌ | |
| View-once messages sending | ❌ | |
| Document/file sending | ❌ | Images/media path exists, general file UX missing |
| Stickers | ✅ | Receive and send stickers |
| Message reactions | ✅ | |
| Composer emoji inline search | ✅ | |
| Edit sent messages | ✅ | Received message edits are handled too |
| Delete messages | ✅ | |
| Forward messages | ✅ | |
| Star/bookmark messages | ✅ | |
| Archive chats | ✅ | |
| Mute chats | ✅ | |
| Pinned messages | ✅ | |
| Group management | ❌ | No create/invite/admin UI |
| Community management | ❌ |  |
| Calls | ❌ | Voice/video calls unsupported |
| Status/stories | ❌ | |
| Settings UI | ✅ | |
| Account/profile editing | ✅ | Includes privacy settings |
| Import/export backups | ❌ | |
| DB encryption and keyring integration | ❌ | |
| Daemon SNI (Tray) | ❌ | |
  
</details>

## Architecture

Whatevr is one background daemon, `whatevrd`, and a socket. The daemon owns the
WhatsApp connection, the login session, the local SQLite store, the media cache
and desktop notifications, and serves all of it over
[the whatevr protocol](PROTOCOL.md). Frontends never speak to WhatsApp directly
and never keep durable state of their own; several can run at once against the
same daemon, and each sees the same rows in the same order because the daemon
computed that order.

The daemon is Go (`whatevrd/`); the flagship frontend is `whatkevr`, in
C++20/QML on Qt 6 and Kirigami. `whatgevr`, a primitive GTK4/libadwaita
frontend, is unmaintained and excluded from the build. A TUI and a scriptable
CLI are wanted and unclaimed (see *Write a frontend* above); that work needs no
changes to the daemon.

Whatevr will be Linux-first for now until its stable. I am open to contributions for porting functionality to other platforms as long as they don't affect existing performance and Linux functionality significantly. 

## Acknowledgements
Whatevr stands on the shoulders of:

- [whatsmeow](https://github.com/tulir/whatsmeow) — WhatsApp Web multidevice protocol library (MPL-2.0)
- [Qt](https://www.qt.io) — cross-platform application framework (LGPL-3.0)
- [KDE Frameworks](https://kde.org) / [Kirigami](https://develop.kde.org/frameworks/kirigami/) — UI toolkit and helpers (LGPL)
- [Kirigami Addons](https://invent.kde.org/libraries/kirigami-addons) — convergent UI components (LGPL)
- [rlottie](https://github.com/Samsung/rlottie) — Lottie rendering for animated stickers (MIT)
- [emojilib](https://github.com/muan/emojilib) — emoji keyword / shortcode data, © 2014 Mu-An Chiou (MIT)
- [Google Fonts emoji metadata](https://github.com/googlefonts/emoji-metadata) — emoji ordering & grouping data (Apache-2.0)

Additionally, I took a fair amount of inspiration for UI layouts :from [NeoChat](https://apps.kde.org/neochat/)

## License
This program is licensed under the BSD-3-Clause License
