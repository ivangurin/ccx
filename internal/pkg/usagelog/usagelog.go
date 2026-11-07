// Package usagelog records per-request usage as append-only daily JSONL
// under the daemon's log directory: the daemon is the only place that sees
// every request, and "what did this provider/model cost me today" is not
// answerable anywhere else. Counts and timings only — never bodies, never
// tokens or credentials.
package usagelog

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one request's usage record.
type Entry struct {
	Ts           time.Time `json:"ts"`
	Provider     string    `json:"provider"`
	Protocol     string    `json:"protocol"`
	Model        string    `json:"model"`
	Stream       bool      `json:"stream"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	LatencyMS    int64     `json:"latency_ms"`
	TTFTMS       int64     `json:"ttft_ms"` // time to first token; 0 for non-stream
	Status       int       `json:"status"`
	Error        string    `json:"error,omitempty"`
}

// Recorder sinks entries. Implementations must be safe for concurrent use
// and must never fail a request over a logging problem.
type Recorder interface {
	Record(entry Entry)
}

// Noop drops everything (the zero wiring: nothing recorded until serve mode
// installs the file recorder).
type Noop struct{}

// Record drops the entry.
func (Noop) Record(Entry) {}

// Default is the sink Record writes to; serve mode replaces it with a file
// recorder. Swappable for tests.
var Default Recorder = Noop{}

// Record hands the entry to Default.
func Record(e Entry) {
	if Default == nil {
		return
	}
	Default.Record(e)
}

// FileRecorder appends one JSON line per entry to
// <dir>/usage-YYYY-MM-DD.jsonl (0600, opened per write — request volume,
// no handle to leak). Failures are logged once and swallowed: usage
// accounting must never break a request.
type FileRecorder struct {
	dir string
	mu  sync.Mutex

	warned bool
}

// NewFileRecorder builds a recorder over the log directory (created lazily
// by the first write — a fresh install records nothing until it serves).
func NewFileRecorder(dir string) *FileRecorder {
	return &FileRecorder{dir: dir}
}

// Record appends the entry to today's file.
func (r *FileRecorder) Record(e Entry) {
	if e.Ts.IsZero() {
		e.Ts = time.Now().UTC()
	}
	data, err := json.Marshal(e) //nolint:errchkjson // entry marshals by construction
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	path := filepath.Join(r.dir, "usage-"+e.Ts.UTC().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // fixed dir from config
	if err != nil {
		r.warnOnce(err)

		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		r.warnOnce(err)
	}
}

func (r *FileRecorder) warnOnce(err error) {
	if r.warned {
		return
	}
	r.warned = true
	slog.Warn("usagelog: dropping entries", "error", err)
}
