package protocol

import (
	"log"
	"os"
	"time"
)

// The protocol layer's own logging is otherwise error-only, which left the
// serving side of a chat open completely unmeasured: a `group_members` Open
// that spent half a second resolving a roster looked identical to one that
// returned instantly. WHATEVRD_PROTOCOL_PERF=1 turns on duration logging for
// the two places that actually take time — opening a view and recomputing a
// window — so a slow frontend can be attributed instead of guessed at.
//
// slowOpen is logged unconditionally (not only under the env var): an Open
// this slow is a bug worth seeing in a default-level log, and it fires at most
// once per subscription.
var protocolPerf = os.Getenv("WHATEVRD_PROTOCOL_PERF") == "1"

// slowOpenThreshold mirrors store.slowOpThreshold: an Open that blocks this
// long is a latency bug regardless of who asked for it.
const slowOpenThreshold = 100 * time.Millisecond

func logViewOpen(view string, start time.Time) {
	d := time.Since(start)
	switch {
	case d >= slowOpenThreshold:
		log.Printf("protocol: slow view open %q took %s", view, d.Round(time.Millisecond))
	case protocolPerf:
		log.Printf("protocol: view open %q took %s", view, d.Round(time.Microsecond))
	}
}

func logRecompute(sub int64, items int, start time.Time) {
	if !protocolPerf {
		return
	}
	log.Printf("protocol: recompute sub %d over %d items took %s",
		sub, items, time.Since(start).Round(time.Microsecond))
}
