> _This project is not affiliated with WhatsApp or Meta._
# Whatevr
A Linux-first native client for WhatsApp. It uses [whatsmeow](https://github.com/tulir/whatsmeow) to access the WhatsApp web multidevice API.

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
Currently you can only get Whatevr by building it (see Building below). Packaging
recipes for Flatpak, Arch, Debian and RPM live under [`packaging/`](packaging/);


## Building
<details>
    <summary>Build Instructions</summary>
    
whatevr builds through a single top-level `Makefile` that compiles **both** the
daemon (`whatevrd`) and the Qt/Kirigami frontend (`whatkevr`). The daemon must be
running for any frontend to work. Before building, check the
[Platform support](#platform-support) floors above — on distros that are too old
(e.g. Ubuntu LTS) use Flatpak instead of a source build.

#### 1. Install dependencies

**Daemon:** Go 1.25+, a C compiler, SQLite dev files, pkg-config.
**Frontend:** C++20 compiler, CMake 3.21+, Ninja, Qt 6.8+, KDE Frameworks 6.5+
(KCoreAddons, KDBusAddons, KI18n, Kirigami, Prison), Kirigami Addons 1.0+,
rlottie.

```sh
# Arch
sudo pacman -S --needed base-devel go sqlite pkgconf cmake ninja \
  extra-cmake-modules qt6-base qt6-declarative qt6-shadertools qt6-grpc \
  kcoreaddons kdbusaddons ki18n kirigami kirigami-addons prison rlottie

# Fedora
sudo dnf install go gcc gcc-c++ sqlite-devel pkgconf-pkg-config cmake ninja-build \
  extra-cmake-modules qt6-qtbase-devel qt6-qtdeclarative-devel \
  qt6-qtshadertools-devel qt6-qtgrpc-devel kf6-kcoreaddons-devel \
  kf6-kdbusaddons-devel kf6-ki18n-devel kf6-kirigami-devel kf6-prison-devel \
  kf6-kirigami-addons-devel rlottie-devel

# Debian 13 "trixie" (needs Go >= 1.25 — see Platform support)
sudo apt install golang gcc g++ libsqlite3-dev pkg-config cmake ninja-build \
  extra-cmake-modules qt6-base-dev qt6-declarative-dev qt6-shadertools-dev \
  qt6-grpc-dev libkf6coreaddons-dev libkf6dbusaddons-dev libkf6i18n-dev \
  libkf6kirigami-dev libkf6prison-dev kirigami-addons-dev librlottie-dev
```

#### 2. Build and install

```sh
make build                            # build daemon + frontend
make install PREFIX="$HOME/.local"    # user-local install
# or system-wide:
sudo make install PREFIX=/usr
```

`make install` places the `whatevrd` and `whatkevr` binaries, desktop entry,
icon, AppStream metainfo and the systemd user units under `PREFIX`. Make sure the
chosen `bin` directory is on your `PATH` (e.g. `~/.local/bin`).

Other handy targets: `make version`, `make validate`, `make clean`, and the
packaging targets `make package-{flatpak,arch,deb,rpm}`.

#### 3. Run

Start the daemon, then the frontend:

```sh
whatevrd      # or run it via systemd (below)
whatkevr
```

#### Run the daemon via systemd (optional)

`make install` ships two **mutually exclusive** user units — enable **one**,
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
Makefile and packaging. Build it manually if you want to hack on it:

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
| Group chats | ✅ | Basic chat/message support |
| Chat avatars | ✅ | |
| Media preview/display | ✅ | Images and cached media |
| Paste image from clipboard | ✅ | |
| Typing indicator | ✅ | Send/receive composing state |
| Online/last-seen presence | ✅ | |
| Offline/history sync progress | ✅ | |
| Desktop notifications | ✅ | Handled by daemon |
| Emoji picker | ✅ | Frontend-local |
| Message search | ❌ | |
| Chat search | ❌ | |
| Contact search/new chat | ❌ | |
| Voice messages | ❌ | |
| Audio playback | ❌ | |
| Video playback | ❌ | |
| View-once messages sending | ❌ | |
| Document/file sending | ❌ | Images/media path exists, general file UX missing |
| Stickers | ⚠️ | Can receive any type of sticker. Sending yet to be implmented |
| Message reactions | ❌ | |
| Composer emoji inline search | ❌ | |
| Edit sent messages | ❌ | Even received messages are not edited |
| Delete messages | ❌ | |
| Forward messages | ❌ | |
| Star/bookmark messages | ❌ | |
| Group management | ❌ | No create/invite/admin UI |
| Community management | ❌ |  |
| Calls | ❌ | Voice/video calls unsupported |
| Status/stories | ❌ | |
| Settings UI | ❌ | |
| Account/profile editing | ❌ | |
| Import/export backups | ❌ | |
| DB encryption and keyring integration | ❌ | |
  
</details>

## Architecture
Whatevr is built around a single background daemon, `whatevrd`. The daemon owns WhatsApp connection, login session, local SQLite store, media cache, notifications, and local RPC API. Frontends connect to that daemon instead of speaking to WhatsApp directly.

This approach lets multiple frontends share the same backend. Currently frontend is mainly focused on the Qt/Kirigami frontend, `whatkevr`. There is a primitive GTK4/libadwaita frontend, `whatgevr`, but I will not be working on that for a while (see for my reasons).\
I also have a TUI frontend and a scriptable CLI in mind, though they are far into the future, feel free to take up the task if you feel qualified.

Whatevr will be Linux-first for now until its stable. I am open to contributions for porting functionality to other platforms as long as they don't affect existing performance and Linux functionality significantly. 

## Acknowledgements
Whatevr uses [whatsmeow](https://github.com/tulir/whatsmeow) to access WhatsApp web multidevice API\
...

## License
This program is licensed under the BSD-3-Clause License
