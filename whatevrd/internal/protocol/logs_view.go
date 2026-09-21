package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"whatevrd/internal/logfile"
)

// LogsTailer supplies the `daemon_logs` view its lines. DaemonActions
// implements it on top of the in-memory ring; the rotated files on disk hold
// more history than the ring does.
type LogsTailer interface {
	RecentLogs(ctx context.Context, limit int) ([]string, error)
}

const (
	logsViewMaxLimit  = logfile.MaxLines
	logsRefreshPeriod = 500 * time.Millisecond
	logsDefaultLimit  = 200
)

// logsView is the daemon log tail as a live view: the newest `limit` lines of
// the in-memory ring, oldest first. The `daemon.logs` command answers the same
// lines for a one-shot query; this view exists so the frontend logs page can
// hold a subscription like every other list and watch new lines appear as the
// daemon logs them.
//
// There is no daemon event for log churn, so the session re-runs the query on
// a short ticker and lets the window recomputation notice changes.
type logsView struct {
	tailer LogsTailer
}

func (v logsView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.tailer == nil {
		return nil, nil, errorf(CodeInvalidParams, "daemon logs unavailable")
	}
	limit := logsDefaultLimit
	if len(params) > 0 {
		var p struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(params, &p); err == nil && p.Limit > 0 {
			limit = p.Limit
		}
	}
	if limit > logsViewMaxLimit {
		limit = logsViewMaxLimit
	}
	s := &logsSession{
		tailer:     v.tailer,
		limit:      limit,
		invalidate: invalidate,
		done:       make(chan struct{}),
		seqs:       make(map[string]uint64),
	}
	go s.tail()
	return s, nil, nil
}

type logsSession struct {
	tailer     LogsTailer
	limit      int
	invalidate func()
	done       chan struct{}
	closeOnce  sync.Once

	// Stable identity for content-keyed lines. Log lines have no natural id
	// and the ring slides (every append shifts every position), so keying rows
	// by position would churn the whole window on each new line. Instead each
	// distinct raw line keeps the sequence number first assigned to it; a
	// refresh only appends rows for unseen lines and drops rows whose line
	// left the ring. Duplicate lines (identical text does happen) get ordinal
	// keys, so every occurrence still has its own stable row.
	mu      sync.Mutex
	seqs    map[string]uint64
	nextSeq uint64
}

// tail nudges the window on a ticker. The period bounds how stale the page can
// look; each tick re-reads at most a few hundred short lines from the ring.
func (s *logsSession) tail() {
	tick := time.NewTicker(logsRefreshPeriod)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-tick.C:
			s.invalidate()
		}
	}
}

// Items returns up to `limit` recent lines, oldest first. `max` is ignored:
// the caller's page size and the view's limit both describe the same bound,
// and the ring caps the real size anyway.
func (s *logsSession) Items(int) []Item {
	lines, err := s.tailer.RecentLogs(context.Background(), s.limit)
	if err != nil {
		log.Printf("protocol: daemon_logs view: %v", err)
		lines = nil
	}
	if len(lines) > s.limit {
		lines = lines[len(lines)-s.limit:]
	}
	return s.assign(lines)
}

// assign maps the refresh's lines to stable (id, sort) pairs, retiring keys
// that left the window. Items serializes through mu.
func (s *logsSession) assign(lines []string) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]int, len(lines))
	items := make([]Item, 0, len(lines))
	// Walk oldest → newest so sequence order equals log order.
	for _, line := range lines {
		seen[line]++
		count := seen[line]
		// The first copy of a line keeps the raw text as its stable key;
		// further copies of the same line get ordinal keys so each still
		// carries its own row without disturbing the first one's identity.
		key := line
		if count > 1 {
			key = fmt.Sprintf("%s\x00#%d", line, count)
		}
		seq, ok := s.seqs[key]
		if !ok {
			s.nextSeq++
			seq = s.nextSeq
			s.seqs[key] = seq
		}
		items = append(items, Item{
			ID:   fmt.Sprintf("l:%d", seq),
			Sort: fmt.Sprintf("%020d", seq),
			Data: withLogID(parseLogLine(line), fmt.Sprintf("l:%d", seq)),
		})
	}

	// Retire keys that dropped out of the window, so a long-lived
	// subscription does not accumulate every line it has ever seen. Keys
	// that reappear later simply mint a fresh sequence. Ordinal duplicate
	// keys map back to their raw line for the liveness check.
	for key := range s.seqs {
		raw := key
		if i := strings.IndexByte(key, 0); i >= 0 {
			raw = key[:i]
		}
		if _, keep := seen[raw]; !keep {
			delete(s.seqs, key)
		}
	}
	return items
}

// withLogID stamps the envelope id into the row data (see logsItem).
func withLogID(item logsItem, id string) logsItem {
	item.ID = id
	return item
}

// parseLogLine splits a std-log line ("2006/01/02 15:04:05 text") into the
// fields the logs page renders. Lines that do not start with the standard
// stamp (multi-line continuations) pass through with an empty time; level is
// a best-effort keyword scan.
func parseLogLine(line string) logsItem {
	item := logsItem{Text: line}
	if len(line) >= 20 && line[4] == '/' && line[7] == '/' && line[10] == ' ' && line[13] == ':' {
		item.Time = line[:19]
		item.Text = strings.TrimLeft(line[19:], " ")
	}
	upper := strings.ToUpper(item.Text)
	switch {
	case strings.Contains(upper, "ERROR"), strings.Contains(upper, "FATAL"), strings.Contains(upper, "PANIC"):
		item.Level = "error"
	case strings.Contains(upper, "WARN"):
		item.Level = "warn"
	case strings.Contains(upper, "DEBUG"), strings.Contains(upper, "TRACE"):
		item.Level = "debug"
	default:
		item.Level = "info"
	}
	return item
}

func (s *logsSession) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// logsItem is one log row: the daemon-assigned stable id plus the std-log
// timestamp, a best-effort severity, and the message text. The id inside Data
// is what CollectionViewModel keys rows by (rule 3): without it every row is
// dropped and the Logs tab stays empty.
type logsItem struct {
	ID    string `json:"id"`
	Time  string `json:"time"`
	Level string `json:"level"`
	Text  string `json:"text"`
}
