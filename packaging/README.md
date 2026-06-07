# Packaging

This directory holds the packaging recipes for whatevr. All of them build the
**daemon (`whatevrd`)** and the **Qt/Kirigami frontend (`whatkevr`)** together
and delegate the actual build/install to the top-level [`Makefile`](../Makefile),
so build logic lives in exactly one place. The unmaintained `whatgevr` GTK
frontend is intentionally **not** packaged.

| Format | Files | Build with |
| --- | --- | --- |
| Arch / AUR | [`arch/PKGBUILD`](arch/) (`whatevr-git`) | `make package-arch` |

## Version floors (keep recipes in sync with these)

- **Qt 6.8+** — Qt GRPC/Protobuf is only stable from 6.8 LTS (hard floor).
- **KDE Frameworks 6.5+**, **Kirigami Addons 1.0+**, **CMake 3.21+**.
- **Go 1.25+** — required by the pinned `whatsmeow`. This is the most
  time-restrictive dependency and the main gotcha for packaging: a builder using
  `GOTOOLCHAIN=local` needs a real Go 1.25 toolchain available, not just whatever
  the distro ships.

## Dependency name cross-reference

Build/runtime dependencies of `whatkevr` + `whatevrd`, on Arch:

| Component | Arch |
| --- | --- |
| Go toolchain (≥1.25) | `go` |
| C compiler | `gcc` |
| SQLite (CGO) | `sqlite` |
| CMake ≥3.21 | `cmake` |
| Ninja | `ninja` |
| ECM | `extra-cmake-modules` |
| Qt Base | `qt6-base` |
| Qt Declarative | `qt6-declarative` |
| Qt GRPC/Protobuf | `qt6-grpc` |
| Qt ShaderTools | `qt6-shadertools` |
| KF CoreAddons | `kcoreaddons` |
| KF DBusAddons | `kdbusaddons` |
| KF I18n | `ki18n` |
| KF Kirigami | `kirigami` |
| KF Prison | `prison` |
| Kirigami Addons | `kirigami-addons` |
| rlottie | `rlottie` |

## Notes

- The systemd user unit is generated from
  [`systemd/whatevrd.service.in`](systemd/) and installed to
  `/usr/lib/systemd/user/whatevrd.service`, shipped **disabled** (per distro
  policy). Users enable it with `systemctl --user enable --now whatevrd.service`.
