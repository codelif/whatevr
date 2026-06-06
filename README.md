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
Currently you can only get Whatevr by building it

## Building
<details>
    <summary>Build Instructions</summary>
    
Build and run the daemon first. Frontends expect `whatevrd` to be running.

### daemon

Dependencies:

- Go 1.25+
- C compiler
- SQLite development files
- pkg-config

Arch/Fedora/Debian examples:

```sh
sudo pacman -S --needed base-devel go sqlite pkgconf
sudo dnf install go gcc sqlite-devel pkgconf-pkg-config
sudo apt install golang gcc libsqlite3-dev pkg-config
```

Build:

```sh
cd whatevrd
go build -o ~/.local/bin/whatevrd ./cmd/whatevrd
```

Run:

```sh
~/.local/bin/whatevrd
```

Optional systemd user service:

```sh
mkdir -p ~/.config/systemd/user
cp packaging/systemd/whatevrd.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now whatevrd.service
```

### Qt frontend: whatkevr

Dependencies:

- C++20 compiler
- CMake 3.16+
- Ninja
- Qt 6.11+
- KDE Frameworks 6.25+
- Kirigami Addons
- rlottie

Arch/Fedora/Debian examples:

```sh
sudo pacman -S --needed base-devel cmake ninja extra-cmake-modules qt6-base qt6-declarative qt6-shadertools qt6-grpc kcoreaddons ki18n kirigami kirigami-addons prison rlottie
sudo dnf install gcc-c++ cmake ninja-build extra-cmake-modules qt6-qtbase-devel qt6-qtdeclarative-devel qt6-qtshadertools-devel qt6-qtgrpc-devel kf6-kcoreaddons-devel kf6-ki18n-devel kf6-kirigami-devel kf6-prison-devel kf6-kirigami-addons-devel rlottie-devel
sudo apt install g++ cmake ninja-build extra-cmake-modules
```

Debian/Ubuntu may not ship new enough Qt/KDE packages.

Build:

```sh
cd whatkevr
cmake -B build -G Ninja -DCMAKE_BUILD_TYPE=RelWithDebInfo -DCMAKE_INSTALL_PREFIX="$HOME/.local"
cmake --build build
cmake --install build
```

Run:

```sh
whatkevr
```

### GTK frontend: whatgevr

Dependencies:

- Rust
- GTK4 development files
- libadwaita development files
- pkg-config

Arch/Fedora/Debian examples:

```sh
sudo pacman -S --needed rust gtk4 libadwaita pkgconf
sudo dnf install rust cargo gtk4-devel libadwaita-devel pkgconf-pkg-config
sudo apt install rustc cargo libgtk-4-dev libadwaita-1-dev pkg-config
```

Build:

```sh
cd whatgevr
cargo build --release
install -Dm755 target/release/whatevr ~/.local/bin/whatevr
```

Run:

```sh
whatevr
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
| Document/file sending | ❌ | Images/media path exists, general file UX missing |
| Stickers | ⚠️ | Can receive any type of sticker. Sending yet to be implmented |
| Message reactions | ❌ | |
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
