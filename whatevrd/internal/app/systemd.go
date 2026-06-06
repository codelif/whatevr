package app

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
)

// sdListenFDsStart is the first file descriptor passed by systemd socket
// activation (stdin/stdout/stderr occupy 0..2).
const sdListenFDsStart = 3

// SystemdListener returns the listening socket inherited from systemd socket
// activation, or (nil, nil) when the daemon was not socket-activated.
//
// It implements the sd_listen_fds(3) protocol directly so the project keeps a
// minimal dependency set. The LISTEN_* environment variables are always cleared
// so they are never inherited by child processes (e.g. the notification helper
// that shells out to open a chat).
func SystemdListener() (net.Listener, error) {
	pidStr := os.Getenv("LISTEN_PID")
	fdsStr := os.Getenv("LISTEN_FDS")
	// Clear immediately; these must not leak to any forked child.
	defer func() {
		_ = os.Unsetenv("LISTEN_PID")
		_ = os.Unsetenv("LISTEN_FDS")
		_ = os.Unsetenv("LISTEN_FDNAMES")
	}()

	if pidStr == "" || fdsStr == "" {
		return nil, nil
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid != os.Getpid() {
		// Not meant for this process.
		return nil, nil
	}

	n, err := strconv.Atoi(fdsStr)
	if err != nil || n < 1 {
		return nil, nil
	}
	if n > 1 {
		return nil, fmt.Errorf("expected exactly one socket from systemd, got %d", n)
	}

	fd := sdListenFDsStart
	syscall.CloseOnExec(fd)

	file := os.NewFile(uintptr(fd), "whatevrd-activation-socket")
	if file == nil {
		return nil, fmt.Errorf("invalid socket-activation file descriptor %d", fd)
	}
	defer file.Close()

	listener, err := net.FileListener(file)
	if err != nil {
		return nil, fmt.Errorf("adopt systemd socket: %w", err)
	}
	return listener, nil
}
