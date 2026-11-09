package usagelog

import (
	"log/slog"
	"time"
)

// SlogRecorder writes entries through the daemon's own slog logger instead
// of a file of its own: the unified daemon log (daemon-<launch time>.log)
// carries every stream as JSONL lines distinguished by the `kind` attribute,
// and usage is `kind=usage`. The logger MUST be pre-attributed
// (`log.With("kind", "usage")`) — the record itself only sets the message.
type SlogRecorder struct {
	log *slog.Logger
}

// NewSlogRecorder builds a recorder over the given logger. nil drops every
// entry (same never-break-a-request contract as FileRecorder).
func NewSlogRecorder(log *slog.Logger) *SlogRecorder {
	return &SlogRecorder{log: log}
}

// Record emits the entry as one structured log line. Failures are swallowed
// by slog's handler contract — usage accounting never breaks a request.
func (r *SlogRecorder) Record(e Entry) {
	if r == nil || r.log == nil {
		return
	}
	if e.Ts.IsZero() {
		e.Ts = time.Now().UTC()
	}
	attrs := []any{
		"ts", e.Ts,
		"provider", e.Provider,
		"protocol", e.Protocol,
		"model", e.Model,
		"stream", e.Stream,
		"input_tokens", e.InputTokens,
		"output_tokens", e.OutputTokens,
		"latency_ms", e.LatencyMS,
		"ttft_ms", e.TTFTMS,
		"status", e.Status,
	}
	if e.Error != "" {
		attrs = append(attrs, "error", e.Error)
	}
	r.log.Info("usage", attrs...)
}
