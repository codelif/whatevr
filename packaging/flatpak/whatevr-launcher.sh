#!/bin/sh
# Flatpak entry point. Unlike a system install there is no systemd user service
# inside the sandbox, so we start the daemon ourselves and then run the
# frontend. Both run in the same sandbox instance and share $XDG_RUNTIME_DIR,
# which is where whatevrd creates its gRPC socket.
set -e

whatevrd &
daemon_pid=$!

# Stop the daemon when the frontend exits.
trap 'kill "$daemon_pid" 2>/dev/null || true' EXIT INT TERM

exec whatkevr "$@"
