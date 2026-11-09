package usagelog

import (
	"log/slog"
	"testing"
	"time"

	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// singleLine — the one JSONL line the capture holds (fails otherwise).
func singleLine(t *testing.T, buf *suite_provider.Capture) map[string]any {
	t.Helper()
	lines := buf.Lines(t)
	require.Len(t, lines, 1, "expected exactly one line")

	return lines[0]
}

// The unified-daemon-log contract: a usage entry rides the daemon logger as
// one JSONL line carrying the kind discriminator plus the Entry fields —
// `jq 'select(.kind=="usage")' logs/daemon-*.log` replaces the per-day
// usage-YYYY-MM-DD.jsonl file.
func TestSlogRecorder_WritesUsageLineWithKind(t *testing.T) {
	var buf suite_provider.Capture
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	rec := NewSlogRecorder(log.With("kind", "usage"))

	rec.Record(Entry{
		Ts:       time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		Provider: "work-relay", Protocol: "openai", Model: "gpt-6.1", Stream: true,
		InputTokens: 100, OutputTokens: 7, LatencyMS: 1500, TTFTMS: 300, Status: 200,
	})

	line := singleLine(t, &buf)
	require.Equal(t, "usage", line["kind"])
	require.Equal(t, "usage", line["msg"])
	require.Equal(t, "work-relay", line["provider"])
	require.Equal(t, "openai", line["protocol"])
	require.Equal(t, "gpt-6.1", line["model"])
	require.Equal(t, true, line["stream"])
	require.EqualValues(t, 100, line["input_tokens"])
	require.EqualValues(t, 7, line["output_tokens"])
	require.EqualValues(t, 1500, line["latency_ms"])
	require.EqualValues(t, 300, line["ttft_ms"])
	require.EqualValues(t, 200, line["status"])

	// ts stays a top-level field with the RFC3339 shape the old JSONL had —
	// usage readers parse it, slog's own time is the WRITE moment, not the
	// request start
	ts, ok := line["ts"].(string)
	require.True(t, ok, "ts must be a string, got %T", line["ts"])
	parsed, err := time.Parse(time.RFC3339, ts)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), parsed)
}

// error rides omitempty semantics: absent when empty, present when set.
func TestSlogRecorder_ErrorFieldOmitEmpty(t *testing.T) {
	var buf suite_provider.Capture
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	rec := NewSlogRecorder(log.With("kind", "usage"))

	rec.Record(Entry{Provider: "p", Status: 200})
	lines := buf.Lines(t)
	require.Len(t, lines, 1)
	_, has := lines[0]["error"]
	require.False(t, has, "empty error must not appear")

	rec.Record(Entry{Provider: "p", Status: 429, Error: "rate limited"})
	lines = buf.Lines(t)
	require.Len(t, lines, 2)
	line := lines[1]
	require.Equal(t, "rate limited", line["error"])
	require.EqualValues(t, 429, line["status"])
}

// A zero Ts stamps "now" — the recorder never trusts the caller (the old
// FileRecorder contract, kept).
func TestSlogRecorder_ZeroTsDefaultsToNow(t *testing.T) {
	var buf suite_provider.Capture
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	rec := NewSlogRecorder(log.With("kind", "usage"))

	rec.Record(Entry{Status: 200})

	line := singleLine(t, &buf)
	ts, ok := line["ts"].(string)
	require.True(t, ok)
	_, err := time.Parse(time.RFC3339, ts)
	require.NoError(t, err)
}

// A nil logger drops the entry — usage accounting must never break a request.
func TestSlogRecorder_NilLoggerSwallows(t *testing.T) {
	require.NotPanics(t, func() { NewSlogRecorder(nil).Record(Entry{Status: 200}) })
}

// Record dispatches through Default — the seam every translator calls
// through. Default is Noop unwired (safe outside serve mode), swappable for
// tests.
func TestRecordDispatchesThroughDefault(t *testing.T) {
	var buf suite_provider.Capture
	old := Default
	Default = NewSlogRecorder(slog.New(slog.NewJSONHandler(&buf, nil)).With("kind", "usage"))
	t.Cleanup(func() { Default = old })

	require.NotPanics(t, func() { Record(Entry{Provider: "p", Status: 200}) })

	line := singleLine(t, &buf)
	require.Equal(t, "p", line["provider"])

	// a nil Default is safe too (defensive: the package variable is exported)
	Default = nil
	require.NotPanics(t, func() { Record(Entry{Status: 200}) })
}
