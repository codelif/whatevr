package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"go.mau.fi/whatsmeow"
)

// usesFlag reports whether args has -name or any -name-*.
func usesFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		arg = strings.TrimLeft(arg, "-")
		if arg == name || strings.HasPrefix(arg, name+"=") || strings.HasPrefix(arg, name+"-") {
			return true
		}
	}
	return false
}

// mockClocks are the clocks a mock run's daemon reads instead of the
// machine's: wall for timers and backoff, stamp for what the core stamps on
// arrivals. nil outside a mock run.
type mockClocks struct {
	wall, stamp func() time.Time
}

// clientHooks is what a capture or the send guard hangs on every whatsmeow
// client, and the transport the daemon's own http goes through. zero is none.
type clientHooks struct {
	client    func(*whatsmeow.Client)
	transport http.RoundTripper
}

// qrSource is where a mock run's phone reads the QR it scans.
type qrSource interface {
	QRCodes() (<-chan string, func())
}

// tap sees every frame on the socket, for captures.
type tap = func(conn uint64, dir string, frame []byte)

// debugFlags are the daemon's own, never a command's: no persistence into
// subcommands, which have flags of the same names.
func debugFlags(flags ...cli.Flag) []cli.Flag {
	for _, f := range flags {
		switch f := f.(type) {
		case *cli.StringFlag:
			f.Local, f.Category = true, "debug"
		case *cli.BoolFlag:
			f.Local, f.Category = true, "debug"
		case *cli.Int64Flag:
			f.Local, f.Category = true, "debug"
		case *cli.IntFlag:
			f.Local, f.Category = true, "debug"
		case *cli.DurationFlag:
			f.Local, f.Category = true, "debug"
		case *cli.FloatFlag:
			f.Local, f.Category = true, "debug"
		case *cli.StringSliceFlag:
			f.Local, f.Category = true, "debug"
		default:
			panic(fmt.Sprintf("debugFlags: %T", f))
		}
	}
	return flags
}
