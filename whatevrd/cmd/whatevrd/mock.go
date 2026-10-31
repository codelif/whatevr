//go:build whatevr_mock

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/codelif/whatevr/platform"
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"

	"whatevrd/internal/app"
	"whatevrd/internal/capture"
	"whatevrd/internal/model"
	"whatevrd/internal/wamock"
)

// markerName tags a directory as one this binary created and may therefore
// wipe. Without it a mistyped --mock-dir could delete something that matters.
const markerName = ".whatevr-mock"

type mockRun struct {
	scenario wamock.Scenario
	dir      string
	opts     wamock.Options
	notify   bool
	faults   []string
}

// mockSilencesNotifications reports whether this run should keep its hands off
// the session bus. A scenario is synthetic traffic, and a stress scenario is
// hundreds of messages a second: a desktop toast for any of it is wrong, and
// the frontends are what the mock exists to exercise anyway. --mock-notify puts
// them back for the one case where the notifier itself is the thing under test.
func mockSilencesNotifications(run *mockRun) bool {
	return run != nil && !run.notify
}

type mockFlagSet struct {
	scenario, dir, phone, control, now, capture *string
	list, keep, notify                          *bool
	faults                                      *[]string
	seed                                        *int64
	scanDelay, histDelay, olderDelay, gate      *time.Duration
	segment                                     *int
	speed                                       *float64
}

func mockFlags() (*mockFlagSet, []cli.Flag) {
	f := &mockFlagSet{
		scenario: new(string), dir: new(string), phone: new(string), control: new(string), now: new(string), capture: new(string),
		list: new(bool), keep: new(bool), notify: new(bool),
		faults:    new([]string),
		seed:      new(int64),
		scanDelay: new(time.Duration), histDelay: new(time.Duration), olderDelay: new(time.Duration), gate: new(time.Duration),
		segment: new(int),
		speed:   new(float64),
	}
	return f, debugFlags(
		&cli.StringFlag{Name: "mock", Destination: f.scenario, Usage: "run against a fake WhatsApp server using this scenario"},
		&cli.BoolFlag{Name: "mock-list", Destination: f.list, Usage: "list mock scenarios and exit"},
		&cli.StringFlag{Name: "mock-dir", Destination: f.dir, Usage: "scratch directory for mock state (default: platform runtime directory)"},
		&cli.Int64Flag{Name: "mock-seed", Value: 1, Destination: f.seed, Usage: "seed for every key and identifier the mock generates"},
		&cli.DurationFlag{Name: "mock-scan-delay", Destination: f.scanDelay, Usage: "how long a published QR sits unscanned before the mock phone pairs"},
		&cli.StringFlag{Name: "mock-phone", Destination: f.phone, Usage: "phone number the mock account answers as"},
		&cli.BoolFlag{Name: "mock-keep", Destination: f.keep, Usage: "keep existing mock state instead of starting fresh"},
		&cli.DurationFlag{Name: "mock-history-delay", Destination: f.histDelay, Usage: "how long between history sync chunks, to make the sync view watchable"},
		&cli.DurationFlag{Name: "mock-older-delay", Destination: f.olderDelay, Usage: "how long the mock phone takes to answer a request for older history"},
		&cli.StringFlag{Name: "mock-control", Destination: f.control, Usage: "bind a control socket here for the quiescence barrier"},
		&cli.StringFlag{Name: "mock-now", Destination: f.now, Usage: "pin the clock scenario timestamps hang off, as RFC3339, for reproducible frames"},
		&cli.BoolFlag{Name: "mock-notify", Destination: f.notify, Usage: "let a mock run raise desktop notifications"},
		&cli.StringSliceFlag{Name: "mock-fault", Destination: f.faults, Usage: "a network fault in place before the first dial, as the control socket's fault takes them; refuse with --mock-keep serves a copied account offline, untouched"},
		&cli.StringFlag{Name: "mock-capture", Destination: f.capture, Usage: "replay this capture (a name or a path) instead of a scenario"},
		&cli.IntFlag{Name: "mock-segment", Value: 1, Destination: f.segment, Usage: "which segment of --mock-capture this run plays"},
		&cli.FloatFlag{Name: "mock-speed", Destination: f.speed, Usage: "replay pace against the recorded clock, 1 is real time, 0 as fast as the gates allow"},
		&cli.DurationFlag{Name: "mock-gate", Value: 5 * time.Second, Destination: f.gate, Usage: "how long a replayed push waits for the client to catch up"},
	)
}

func mockUnbuilt([]string) string { return "" }

func mockScenario(run *mockRun) string {
	if run == nil {
		return ""
	}
	return run.opts.Scenario
}

// mockPrepare acts on the mock flags and, in mock mode, repoints the XDG
// directories at a scratch tree. It has to run before app.ResolvePaths, so the
// real account database is never opened by a mock daemon.
func mockPrepare(log zerolog.Logger, f *mockFlagSet) *mockRun {
	scenario, list, dir, seed, scanDelay, phone, keep, histDelay, control, now, notify :=
		f.scenario, f.list, f.dir, f.seed, f.scanDelay, f.phone, f.keep, f.histDelay, f.control, f.now, f.notify

	if *list {
		for _, s := range wamock.List() {
			fmt.Printf("%-16s %s\n", s.Name, s.Description)
		}
		os.Exit(0)
	}
	var capturePath string
	if *f.capture != "" {
		if *scenario != "" {
			log.Fatal().Msg("--mock and --mock-capture are two different runs, pick one")
		}
		// names resolve under the real state dir, before it moves
		stateHome, err := app.StateHome()
		if err != nil {
			log.Fatal().Err(err).Msg("resolve state dir")
		}
		if capturePath, err = capture.Resolve(*f.capture, stateHome); err != nil {
			log.Fatal().Err(err).Msg("resolve --mock-capture")
		}
		*scenario = wamock.RecordedScenario
	}
	if *scenario == "" {
		return nil
	}

	found, ok := wamock.Lookup(*scenario)
	if !ok {
		log.Fatal().Str("scenario", *scenario).Msg("unknown mock scenario (try --mock-list)")
	}
	if found.Name == wamock.RecordedScenario && capturePath == "" {
		log.Fatal().Msg("the recorded scenario plays a capture, use --mock-capture")
	}

	root := *dir
	if root == "" {
		runtimeDir, err := platform.RuntimeDir()
		if err != nil {
			log.Fatal().Err(err).Msg("resolve mock runtime directory")
		}
		root = filepath.Join(runtimeDir, "whatevr-mock", *scenario)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		log.Fatal().Err(err).Msg("resolve --mock-dir")
	}
	if err := prepareMockDir(root, *keep); err != nil {
		log.Fatal().Err(err).Msg("prepare mock dir")
	}

	// a socket set for the real daemon is not this one's
	if err := os.Unsetenv(app.SocketEnv); err != nil {
		log.Fatal().Err(err).Msg("unset " + app.SocketEnv)
	}
	// Every XDG base moves, so the socket, the lock, the app database, the
	// whatsmeow session and the run logs all land inside the scratch tree. Two
	// mock runs of different scenarios cannot collide, and neither can touch a
	// real account.
	for env, sub := range map[string]string{
		"XDG_RUNTIME_DIR": "run",
		"XDG_DATA_HOME":   "data",
		"XDG_CACHE_HOME":  "cache",
		"XDG_STATE_HOME":  "state",
		"XDG_CONFIG_HOME": "config",
	} {
		path := filepath.Join(root, sub)
		if err := os.MkdirAll(path, 0o700); err != nil {
			log.Fatal().Err(err).Str("path", path).Msg("create mock dir")
		}
		if err := os.Setenv(env, path); err != nil {
			log.Fatal().Err(err).Str("env", env).Msg("set mock env")
		}
	}

	if err := setInstanceSocket(root); err != nil {
		log.Fatal().Err(err).Msg("resolve mock socket")
	}
	opts := wamock.Options{
		Seed:         *seed,
		Scenario:     found.Name,
		AccountPhone: *phone,
		ScanDelay:    *scanDelay,
		HistoryDelay: *histDelay,
		OlderDelay:   *f.olderDelay,
		Control:      *control,
		Capture:      capturePath,
		Segment:      *f.segment,
		Speed:        *f.speed,
		Gate:         *f.gate,
	}
	if *now != "" {
		at, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			log.Fatal().Err(err).Msg("parse --mock-now")
		}
		opts.Now = at
	}
	return &mockRun{scenario: found, dir: root, opts: opts, notify: *notify, faults: *f.faults}
}

// prepareMockDir makes root exist and, unless asked to keep it, empty. It only
// ever removes a directory carrying our marker file.
func prepareMockDir(root string, keep bool) error {
	info, err := os.Stat(root)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s exists and is not a directory", root)
	case keep:
		// Nothing to do, the caller wants whatever is there.
	default:
		if _, err := os.Stat(filepath.Join(root, markerName)); err != nil {
			return fmt.Errorf("%s was not created by --mock (no %s); refusing to clear it", root, markerName)
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
	}
	marker := filepath.Join(root, markerName)
	return os.WriteFile(marker, []byte("whatevrd --mock scratch directory\n"), 0o600)
}

// mockStart brings up the fake server. It must run before the client exists,
// because whatsmeow snapshots http.DefaultTransport when it builds one.
func mockStart(ctx context.Context, run *mockRun, login qrSource, socket string) (func(), error) {
	if run == nil {
		return func() {}, nil
	}
	run.opts.Login = login
	run.opts.Socket = socket
	srv, err := wamock.New(ctx, run.opts)
	if err != nil {
		return nil, err
	}
	if err := srv.Start(ctx); err != nil {
		return nil, err
	}
	for _, f := range run.faults {
		if err := srv.Fault(wamock.Fault(f), 0); err != nil {
			_ = srv.Close()
			return nil, err
		}
	}
	if err := srv.StartControl(ctx, run.opts.Control); err != nil {
		return nil, err
	}
	zerolog.Ctx(ctx).Info().Str("scenario", run.scenario.Name).Str("dir", run.dir).Msg("mock mode")
	return func() { _ = srv.Close() }, nil
}

// mockTime is the clocks of a mock run: the wall the control socket can
// jump, and the recording's time during a replay.
func mockTime(run *mockRun) *mockClocks {
	if run == nil {
		return nil
	}
	return &mockClocks{wall: wamock.Wall, stamp: wamock.Stamp}
}

// mockIDs pins the ids a mock run hands out to --mock-seed, so a frontend
// that colours by id draws the same frame every run.
func mockIDs(run *mockRun, ids *model.IDs) {
	if run != nil {
		ids.Derive(run.opts.Seed)
	}
}

const mockPathsSupported = true
