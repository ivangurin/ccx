// Package usagelog records per-request usage. Entries ride the daemon's
// unified log (kind=usage JSONL lines through SlogRecorder) — counts and
// timings only, never bodies, never tokens or credentials.
package usagelog

import "time"

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
// installs the logger-backed recorder).
type Noop struct{}

// Record drops the entry.
func (Noop) Record(Entry) {}

// Default is the sink Record writes to; serve mode replaces it with a
// SlogRecorder over the daemon logger. Swappable for tests.
var Default Recorder = Noop{}

// Record hands the entry to Default.
func Record(e Entry) {
	if Default == nil {
		return
	}
	Default.Record(e)
}
