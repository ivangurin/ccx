package reqdump

import (
	"log/slog"
	"testing"

	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// The unified-log contract: a dump rides the given daemon logger as one
// JSONL line with kind=reqdump — no dump-<launch time>.log file is created
// anymore. The line carries the dump kind (anthropic-req, codex-sse, …),
// the sequence number and the body: a valid JSON document embeds verbatim
// (not as a string), keeping jq's .body|fromjson unnecessary.
func TestWrite_RidesLoggerWithKind(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	var buf suite_provider.Capture
	Configure(Options{Enabled: true, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})

	Write("anthropic-req", []byte(`{"model":"m"}`))
	Write("codex-sse", []byte(`{"event":"x"}`))

	lines := buf.Lines(t)
	require.Len(t, lines, 2)
	require.Equal(t, "reqdump", lines[0]["msg"])
	require.Equal(t, "reqdump", lines[0]["kind"])
	require.Equal(t, "reqdump", lines[1]["kind"])
	require.Equal(t, "anthropic-req", lines[0]["dump_kind"])
	require.EqualValues(t, 1, lines[0]["seq"])
	require.EqualValues(t, 2, lines[1]["seq"])

	// a valid JSON body embeds verbatim — a nested object, not a string
	body, ok := lines[0]["body"].(map[string]any)
	require.True(t, ok, "valid JSON body must embed as an object, got %T", lines[0]["body"])
	require.Equal(t, "m", body["model"])
}

// A non-JSON body becomes an escaped JSON string, never a broken JSONL line.
func TestWrite_InvalidBodyAsString(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	var buf suite_provider.Capture
	Configure(Options{Enabled: true, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})

	Write("codex-resp-400", []byte("not json"))

	lines := buf.Lines(t)
	require.Len(t, lines, 1)
	require.Equal(t, "not json", lines[0]["body"])
}

// No logger configured — dumps drop silently (never break the request path).
func TestWrite_NilLoggerDrops(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	Configure(Options{Enabled: true, Logger: nil})
	require.True(t, Enabled())

	require.NotPanics(t, func() { Write("anthropic-req", []byte(`{}`)) })
}

// Disabled by default: Configure without Enabled is a no-op sink even with
// a logger at hand.
func TestWrite_DisabledDrops(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	var buf suite_provider.Capture
	Configure(Options{Enabled: false, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	require.False(t, Enabled())

	Write("anthropic-req", []byte(`{}`))

	require.Empty(t, buf.String())
}

// seq stays monotonic across writes (the old JSONL contract).
func TestWrite_SeqMonotonic(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	var buf suite_provider.Capture
	Configure(Options{Enabled: true, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})

	Write("a", []byte(`{}`))
	Write("b", []byte(`{}`))
	Write("c", []byte(`{}`))

	lines := buf.Lines(t)
	require.Len(t, lines, 3)
	require.Less(t, lines[0]["seq"], lines[1]["seq"])
	require.Less(t, lines[1]["seq"], lines[2]["seq"])
	require.GreaterOrEqual(t, seqForTest(), int64(3))
}
