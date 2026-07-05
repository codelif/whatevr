set shell := ["bash", "-euo", "pipefail", "-c"]

build_dir := "build"
version := `scripts/version.py full`
version_numeric := `scripts/version.py numeric`

default:
    @just --list

# Debug build for local testing.
build dir=build_dir:
    @just _build debug "{{dir}}"

# Optimized release build.
build-release dir=build_dir:
    @just _build release "{{dir}}"

# Build and install an optimized release.
install prefix="/usr/local" destdir="":
    @just _install release "{{prefix}}" "{{destdir}}"

# Build and install a debug build for smoke tests.
install-dev prefix="/usr/local" destdir="":
    @just _install debug "{{prefix}}" "{{destdir}}"

# Build release source/binary artifacts and checksums.
artifacts arch=`uname -m`:
    @just _source-tarball
    @just _binary-tarball "{{arch}}"
    @just _checksums

validate:
    @desktop-file-validate whatkevr/data/in.codelif.Whatevr.desktop
    @appstreamcli validate --no-net whatkevr/data/in.codelif.Whatevr.metainfo.xml
    @xmllint --noout whatkevr/data/in.codelif.Whatevr.xml

proto:
    @protoc \
        --proto_path=proto \
        --go_out=whatevrd --go_opt=module=whatevrd \
        --go-grpc_out=whatevrd --go-grpc_opt=module=whatevrd \
        proto/whatevr.proto

version:
    @printf '%s\n' '{{version}}'

# Update metadata, validate, commit, and tag. Does not push. x.y.z
release version:
    @uv run scripts/release.py "{{version}}"

# Regenerate the bundled chat-wallpaper doodle (seed defaults to the version).
gen-doodle seed=version_numeric:
    @scripts/gen_doodle.py --seed "{{seed}}"

uninstall prefix="/usr/local" destdir="":
    @prefix="{{prefix}}"; \
    destdir="{{destdir}}"; \
    rm -f "$destdir$prefix/bin/whatevrd"; \
    rm -f "$destdir$prefix/bin/whatkevr"; \
    rm -f "$destdir$prefix/lib/systemd/user/whatevrd.service"; \
    rm -f "$destdir$prefix/lib/systemd/user/whatevrd.socket"; \
    rm -f "$destdir$prefix/share/applications/in.codelif.Whatevr.desktop"; \
    rm -f "$destdir$prefix/share/metainfo/in.codelif.Whatevr.metainfo.xml"; \
    rm -f "$destdir$prefix/share/mime/packages/in.codelif.Whatevr.xml"; \
    rm -f "$destdir$prefix/share/icons/hicolor/scalable/apps/in.codelif.Whatevr.svg"

clean:
    @rm -rf {{build_dir}}

_build profile dir=build_dir:
    @test "{{profile}}" = debug -o "{{profile}}" = release
    @just _build-daemon "{{profile}}" "{{dir}}"
    @just _build-frontend "{{profile}}" "{{dir}}"

_build-daemon profile dir=build_dir:
    @profile="{{profile}}"; \
    build_root="{{dir}}"; \
    case "$build_root" in \
        /*) out_dir="$build_root/$profile" ;; \
        *) out_dir="$(pwd)/$build_root/$profile" ;; \
    esac; \
    ldflags="-X whatevrd/internal/rpc.Version={{version}} -X whatevrd/internal/protocol.Version={{version}}"; \
    go_flags=(-buildvcs=false -tags sqlite_fts5); \
    if [ "$profile" = release ]; then \
        go_flags=(-trimpath "${go_flags[@]}"); \
        ldflags="$ldflags -s -w"; \
    fi; \
    mkdir -p "$out_dir"; \
    CGO_ENABLED=1 go -C whatevrd build "${go_flags[@]}" -ldflags "$ldflags" \
        -o "$out_dir/whatevrd" ./cmd/whatevrd

_build-frontend profile dir=build_dir:
    @profile="{{profile}}"; \
    if [ "$profile" = release ]; then build_type=Release; else build_type=Debug; fi; \
    cmake -S whatkevr -B "{{dir}}/$profile/whatkevr" -G Ninja \
        -DCMAKE_BUILD_TYPE="$build_type" \
        -DWHATEVR_VERSION={{version_numeric}} \
        -DWHATEVR_VERSION_FULL={{version}}; \
    cmake --build "{{dir}}/$profile/whatkevr"

_install profile prefix destdir:
    @just _build "{{profile}}"
    @profile="{{profile}}"; \
    prefix="{{prefix}}"; \
    destdir="{{destdir}}"; \
    build_root="{{build_dir}}/$profile"; \
    bindir="$prefix/bin"; \
    user_unit_dir="$prefix/lib/systemd/user"; \
    install -Dm755 "$build_root/whatevrd" "$destdir$bindir/whatevrd"; \
    DESTDIR="$destdir" cmake --install "$build_root/whatkevr" --prefix "$prefix"; \
    sed "s|@BINDIR@|$bindir|g" packaging/systemd/whatevrd.service.in \
        > "$build_root/whatevrd.service"; \
    install -Dm644 "$build_root/whatevrd.service" \
        "$destdir$user_unit_dir/whatevrd.service"; \
    install -Dm644 packaging/systemd/whatevrd.socket \
        "$destdir$user_unit_dir/whatevrd.socket"

_source-tarball:
    @version="{{version}}"; \
    mkdir -p {{build_dir}}; \
    git archive --format=tar --prefix="whatevr-$version/" HEAD \
        > "{{build_dir}}/whatevr-$version.tar"; \
    printf '%s\n' "$version" > {{build_dir}}/VERSION; \
    tar --transform "s,^,whatevr-$version/," \
        -rf "{{build_dir}}/whatevr-$version.tar" -C {{build_dir}} VERSION; \
    gzip -f "{{build_dir}}/whatevr-$version.tar"; \
    printf 'wrote {{build_dir}}/whatevr-%s.tar.gz\n' "$version"

_binary-tarball arch:
    @version="{{version}}"; \
    name="whatevr-$version-linux-{{arch}}"; \
    root="$(pwd)/{{build_dir}}/release/dist-bin-root"; \
    dist_dir="$(pwd)/{{build_dir}}/$name"; \
    rm -rf "$root" "$dist_dir" "$(pwd)/{{build_dir}}/$name.tar.zst"; \
    just _install release /usr "$root"; \
    strip --strip-unneeded "$root/usr/bin/whatevrd"; \
    strip --strip-unneeded "$root/usr/bin/whatkevr"; \
    install -Dm644 LICENSE "$root/usr/share/licenses/whatevr/LICENSE"; \
    mkdir -p "$dist_dir"; \
    cp -a "$root/usr" "$dist_dir/"; \
    tar -C {{build_dir}} -caf "$(pwd)/{{build_dir}}/$name.tar.zst" "$name"; \
    printf 'wrote {{build_dir}}/%s.tar.zst\n' "$name"

_checksums:
    @cd {{build_dir}} && sha256sum whatevr-*.tar.gz whatevr-*.tar.zst > SHA256SUMS
    @printf 'wrote {{build_dir}}/SHA256SUMS\n'
