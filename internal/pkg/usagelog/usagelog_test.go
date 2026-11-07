package usagelog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileRecorder_AppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	rec := NewFileRecorder(dir)

	rec.Record(Entry{
		Ts:       time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC),
		Provider: "work-relay", Protocol: "openai", Model: "gpt-6.1", Stream: true,
		InputTokens: 100, OutputTokens: 7, LatencyMS: 1500, TTFTMS: 300, Status: 200,
	})
	rec.Record(Entry{
		Ts:       time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC),
		Provider: "work-relay", Protocol: "openai", Model: "gpt-6.1",
		LatencyMS: 20, Status: 429, Error: "rate limited",
	})

	path := filepath.Join(dir, "usage-2026-11-07.jsonl")
	f, err := os.Open(path) //nolint:gosec // test sandbox path
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	info, err := f.Stat()
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	lines := bufio.NewScanner(f)
	var entries []Entry
	for lines.Scan() {
		var e Entry
		require.NoError(t, json.Unmarshal(lines.Bytes(), &e))
		entries = append(entries, e)
	}
	require.Len(t, entries, 2)
	require.Equal(t, "work-relay", entries[0].Provider)
	require.Equal(t, 100, entries[0].InputTokens)
	require.EqualValues(t, 300, entries[0].TTFTMS)
	require.Equal(t, 429, entries[1].Status)
	require.Equal(t, "rate limited", entries[1].Error)
}

// Each day gets its own file (cheap rotation, no cleanup process).
func TestFileRecorder_RollsPerDay(t *testing.T) {
	dir := t.TempDir()
	rec := NewFileRecorder(dir)
	rec.Record(Entry{Ts: time.Date(2026, 11, 6, 23, 59, 0, 0, time.UTC), Status: 200})
	rec.Record(Entry{Ts: time.Date(2026, 11, 7, 0, 1, 0, 0, time.UTC), Status: 200})

	for _, name := range []string{"usage-2026-11-06.jsonl", "usage-2026-11-07.jsonl"} {
		_, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
	}
}

// A zero Ts stamps "now" — the recorder never has to trust the caller.
func TestFileRecorder_ZeroTsDefaultsToNow(t *testing.T) {
	dir := t.TempDir()
	rec := NewFileRecorder(dir)
	before := time.Now().UTC()
	rec.Record(Entry{Status: 200})

	path := filepath.Join(dir, "usage-"+before.Format("2006-01-02")+".jsonl")
	_, err := os.Stat(path)
	require.NoError(t, err)
}

// An unwritable directory must never break a request — the entry is dropped.
func TestFileRecorder_UnwritableDirSwallows(t *testing.T) {
	rec := NewFileRecorder(filepath.Join(t.TempDir(), "no", "such", "dir"))
	require.NotPanics(t, func() { rec.Record(Entry{Status: 200}) })
}

// Record goes to Default (Noop by default — safe without wiring).
func TestRecordDefaultNoop(t *testing.T) {
	require.NotPanics(t, func() { Record(Entry{Status: 200}) })
}
