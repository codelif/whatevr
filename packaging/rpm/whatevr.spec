Name:           whatevr
Version:        0.1.0
Release:        1%{?dist}
Summary:        Native WhatsApp client for Linux

License:        BSD-3-Clause
URL:            https://github.com/codelif/whatevr
# Produced by `make dist` (carries an embedded VERSION file).
Source0:        %{name}-%{version}.tar.gz

# Go 1.25 is required by whatsmeow. Builds are offline, so a real Go 1.25
# toolchain must be present (GOTOOLCHAIN=local).
BuildRequires:  golang >= 1.25
BuildRequires:  gcc
BuildRequires:  sqlite-devel
BuildRequires:  cmake >= 3.21
BuildRequires:  ninja-build
BuildRequires:  extra-cmake-modules
BuildRequires:  qt6-qtbase-devel >= 6.8
BuildRequires:  qt6-qtdeclarative-devel >= 6.8
BuildRequires:  qt6-qtgrpc-devel >= 6.8
BuildRequires:  qt6-qtshadertools-devel >= 6.8
BuildRequires:  kf6-kcoreaddons-devel >= 6.5
BuildRequires:  kf6-kdbusaddons-devel >= 6.5
BuildRequires:  kf6-ki18n-devel >= 6.5
BuildRequires:  kf6-kirigami-devel >= 6.5
BuildRequires:  kf6-prison-devel >= 6.5
BuildRequires:  kf6-kirigami-addons-devel >= 1.0
BuildRequires:  rlottie-devel
BuildRequires:  desktop-file-utils
BuildRequires:  libappstream-glib

%description
Whatevr is a Linux-first native client for WhatsApp built around the whatevrd
background daemon, which owns the WhatsApp connection, local store, media cache
and notifications, and exposes a local gRPC API. This package ships the daemon
together with the Qt/Kirigami frontend, whatkevr.

%prep
%autosetup -n %{name}-%{version}

%build
# go-sqlite3 needs CGO; offline build needs a local toolchain + module access.
export CGO_ENABLED=1
export GOTOOLCHAIN=local
%make_build build PREFIX=%{_prefix}

%install
%make_install PREFIX=%{_prefix}

%check
desktop-file-validate %{buildroot}%{_datadir}/applications/in.codelif.Whatevr.desktop
appstreamcli validate --no-net %{buildroot}%{_metainfodir}/in.codelif.Whatevr.metainfo.xml

%files
%license LICENSE
%doc README.md
%{_bindir}/whatevrd
%{_bindir}/whatkevr
%{_datadir}/applications/in.codelif.Whatevr.desktop
%{_metainfodir}/in.codelif.Whatevr.metainfo.xml
%{_datadir}/icons/hicolor/scalable/apps/in.codelif.Whatevr.svg
%{_userunitdir}/whatevrd.service

%changelog
* Sat Jun 06 2026 Harsh Sharma <harsh@codelif.in> - 0.1.0-1
- Initial packaging.
