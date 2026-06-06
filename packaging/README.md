# Packaging

This directory holds the packaging recipes for whatevr. All of them build the
**daemon (`whatevrd`)** and the **Qt/Kirigami frontend (`whatkevr`)** together
and delegate the actual build/install to the top-level [`Makefile`](../Makefile),
so build logic lives in exactly one place. The unmaintained `whatgevr` GTK
frontend is intentionally **not** packaged.

| Format | Files | Build with |
| --- | --- | --- |
| Flatpak | [`flatpak/in.codelif.Whatevr.yaml`](flatpak/) | `make package-flatpak` |
| Arch / AUR | [`arch/PKGBUILD`](arch/) (`whatevr-git`) | `make package-arch` |
| Debian | [`debian/`](debian/) | `make package-deb` |
| RPM | [`rpm/whatevr.spec`](rpm/) | `make package-rpm` |

## Version floors (keep recipes in sync with these)

- **Qt 6.8+** — Qt GRPC/Protobuf is only stable from 6.8 LTS (hard floor).
- **KDE Frameworks 6.5+**, **Kirigami Addons 1.0+**, **CMake 3.21+**.
- **Go 1.25+** — required by the pinned `whatsmeow`. This is the most
  time-restrictive dependency and the main gotcha for distro packaging: any
  builder using `GOTOOLCHAIN=local` (Debian, sandboxed RPM, Flatpak) needs a
  real Go 1.25 toolchain available, not just whatever the distro ships.

## Dependency name cross-reference

Build/runtime dependencies of `whatkevr` + `whatevrd`, by ecosystem:

| Component | Arch | Debian/Ubuntu | Fedora |
| --- | --- | --- | --- |
| Go toolchain (≥1.25) | `go` | `golang-go (>=1.25)` | `golang >= 1.25` |
| C compiler | `gcc` | `gcc` | `gcc` |
| SQLite (CGO) | `sqlite` | `libsqlite3-dev` | `sqlite-devel` |
| CMake ≥3.21 | `cmake` | `cmake` | `cmake` |
| Ninja | `ninja` | `ninja-build` | `ninja-build` |
| ECM | `extra-cmake-modules` | `extra-cmake-modules` | `extra-cmake-modules` |
| Qt Base | `qt6-base` | `qt6-base-dev` | `qt6-qtbase-devel` |
| Qt Declarative | `qt6-declarative` | `qt6-declarative-dev` | `qt6-qtdeclarative-devel` |
| Qt GRPC/Protobuf | `qt6-grpc` | `qt6-grpc-dev` | `qt6-qtgrpc-devel` |
| Qt ShaderTools | `qt6-shadertools` | `qt6-shadertools-dev` | `qt6-qtshadertools-devel` |
| KF CoreAddons | `kcoreaddons` | `libkf6coreaddons-dev` | `kf6-kcoreaddons-devel` |
| KF DBusAddons | `kdbusaddons` | `libkf6dbusaddons-dev` | `kf6-kdbusaddons-devel` |
| KF I18n | `ki18n` | `libkf6i18n-dev` | `kf6-ki18n-devel` |
| KF Kirigami | `kirigami` | `libkf6kirigami-dev` | `kf6-kirigami-devel` |
| KF Prison | `prison` | `libkf6prison-dev` | `kf6-prison-devel` |
| Kirigami Addons | `kirigami-addons` | `kirigami-addons-dev` | `kf6-kirigami-addons-devel` |
| rlottie | `rlottie` | `librlottie-dev` | `rlottie-devel` |

## Notes

- The systemd user unit is generated from
  [`systemd/whatevrd.service.in`](systemd/) and installed to
  `/usr/lib/systemd/user/whatevrd.service`, shipped **disabled** (per distro
  policy). Users enable it with `systemctl --user enable --now whatevrd.service`.
