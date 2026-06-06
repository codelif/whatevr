# whatevr — top-level build & packaging driver.
#
# This is the single entry point for building, installing, and packaging the
# whatevr daemon (whatevrd, Go) and Qt/Kirigami frontend (whatkevr, C++).
# Distro packaging recipes (packaging/*) delegate back to these targets so the
# build logic lives in exactly one place.
#
# Common overrides:
#   PREFIX   install prefix            (default /usr/local; distros pass /usr)
#   DESTDIR  staging root for packaging (default empty)
#
# Note: the daemon REQUIRES CGO (github.com/mattn/go-sqlite3) — a C toolchain
# and sqlite are needed. Never set CGO_ENABLED=0.

PREFIX     ?= /usr/local
DESTDIR    ?=
BUILD_DIR  ?= build

GO         ?= go
CMAKE      ?= cmake
NINJA      ?= ninja

BINDIR      = $(PREFIX)/bin
# systemd user units live under <prefix>/lib/systemd/user for system installs.
USERUNITDIR = $(PREFIX)/lib/systemd/user

# --- Version: git tag is authoritative, VERSION file is the offline fallback. -
VERSION := $(shell \
	if git describe --tags --dirty --always >/dev/null 2>&1; then \
		git describe --tags --dirty --always 2>/dev/null | sed 's/^v//'; \
	elif [ -f VERSION ]; then \
		cat VERSION; \
	else \
		echo 0.0.0-unknown; \
	fi)
# project()/KAboutData numeric form: a strict X.Y.Z that CMake's project()
# accepts. Extract it from the derived version; if that has no semver prefix
# (e.g. an untagged checkout reporting a bare commit hash), fall back to the
# VERSION file, then to 0.0.0.
VERSION_NUMERIC := $(shell \
	v=$$(echo "$(VERSION)" | sed -nE 's/^([0-9]+\.[0-9]+\.[0-9]+).*/\1/p'); \
	if [ -z "$$v" ] && [ -f VERSION ]; then \
		v=$$(sed -nE 's/^([0-9]+\.[0-9]+\.[0-9]+).*/\1/p' VERSION); \
	fi; \
	echo "$${v:-0.0.0}")

GO_LDFLAGS := -X 'whatevrd/internal/rpc.Version=$(VERSION)'

.PHONY: all build build-daemon build-frontend install uninstall \
        proto version dist clean validate \
        package-flatpak package-arch package-deb package-rpm

all: build

build: build-daemon build-frontend

build-daemon:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build -C whatevrd -ldflags "$(GO_LDFLAGS)" \
		-o ../$(BUILD_DIR)/whatevrd ./cmd/whatevrd

build-frontend:
	$(CMAKE) -S whatkevr -B $(BUILD_DIR)/whatkevr -G Ninja \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_INSTALL_PREFIX=$(PREFIX) \
		-DWHATEVR_VERSION=$(VERSION_NUMERIC) \
		-DWHATEVR_VERSION_FULL=$(VERSION)
	$(CMAKE) --build $(BUILD_DIR)/whatkevr

# Install both components plus the systemd user unit (templated to the prefix).
install: build
	install -Dm755 $(BUILD_DIR)/whatevrd $(DESTDIR)$(BINDIR)/whatevrd
	DESTDIR=$(DESTDIR) $(CMAKE) --install $(BUILD_DIR)/whatkevr
	sed 's|@BINDIR@|$(BINDIR)|g' packaging/systemd/whatevrd.service.in \
		> $(BUILD_DIR)/whatevrd.service
	install -Dm644 $(BUILD_DIR)/whatevrd.service \
		$(DESTDIR)$(USERUNITDIR)/whatevrd.service
	install -Dm644 packaging/systemd/whatevrd.socket \
		$(DESTDIR)$(USERUNITDIR)/whatevrd.socket

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/whatevrd
	rm -f $(DESTDIR)$(BINDIR)/whatkevr
	rm -f $(DESTDIR)$(USERUNITDIR)/whatevrd.service
	rm -f $(DESTDIR)$(USERUNITDIR)/whatevrd.socket
	rm -f $(DESTDIR)$(PREFIX)/share/applications/in.codelif.Whatevr.desktop
	rm -f $(DESTDIR)$(PREFIX)/share/metainfo/in.codelif.Whatevr.metainfo.xml
	rm -f $(DESTDIR)$(PREFIX)/share/icons/hicolor/scalable/apps/in.codelif.Whatevr.svg

# Developer convenience: regenerate the checked-in Go protobuf/gRPC stubs.
# Requires protoc, protoc-gen-go (v1.36.x) and protoc-gen-go-grpc (v1.6.x).
proto:
	protoc \
		--proto_path=whatevrd/proto \
		--go_out=whatevrd --go_opt=module=whatevrd \
		--go-grpc_out=whatevrd --go-grpc_opt=module=whatevrd \
		whatevrd/proto/whatevr.proto

version:
	@echo $(VERSION)

# Self-describing source tarball: HEAD archive + an injected VERSION file so a
# .git-less tarball still resolves its version.
dist:
	@mkdir -p $(BUILD_DIR)
	git archive --format=tar --prefix=whatevr-$(VERSION)/ HEAD \
		> $(BUILD_DIR)/whatevr-$(VERSION).tar
	echo "$(VERSION)" > $(BUILD_DIR)/VERSION
	tar --transform 's,^,whatevr-$(VERSION)/,' \
		-rf $(BUILD_DIR)/whatevr-$(VERSION).tar -C $(BUILD_DIR) VERSION
	gzip -f $(BUILD_DIR)/whatevr-$(VERSION).tar
	@echo "wrote $(BUILD_DIR)/whatevr-$(VERSION).tar.gz"

validate:
	desktop-file-validate whatkevr/data/in.codelif.Whatevr.desktop
	appstreamcli validate --no-net whatkevr/data/in.codelif.Whatevr.metainfo.xml

package-flatpak:
	flatpak-builder --force-clean $(BUILD_DIR)/flatpak \
		packaging/flatpak/in.codelif.Whatevr.yaml

package-arch:
	cd packaging/arch && makepkg -f

package-deb: dist
	dpkg-buildpackage -us -uc -b

package-rpm: dist
	rpmbuild -ba packaging/rpm/whatevr.spec \
		--define "_sourcedir $(CURDIR)/$(BUILD_DIR)"

clean:
	rm -rf $(BUILD_DIR)
