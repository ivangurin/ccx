package suite_provider

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Capture — a mutex-guarded bytes.Buffer for test loggers: production code
// logs from request goroutines while the test reads, and a plain
// bytes.Buffer is a -race FAILURE (caught live on the selfcheck test).
// Every read goes through the mutex; the decoded helpers parse the captured
// JSONL lines.
type Capture struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write implements io.Writer.
func (c *Capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.b.Write(p)
}

// String — the captured text under the mutex.
func (c *Capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.b.String()
}

// Lines — every captured line decoded as a JSON object (an empty capture
// yields nil).
func (c *Capture) Lines(t *testing.T) []map[string]any {
	t.Helper()
	out := c.String()
	if out == "" {
		return nil
	}
	lines := make([]map[string]any, 0, strings.Count(out, "\n"))
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &line), "line: %s", l)
		lines = append(lines, line)
	}

	return lines
}

// JSONLogger builds an info-level JSON slog.Logger over the capture — the
// shape the daemon's unified log has in production.
func JSONLogger(c *Capture) *slog.Logger {
	return slog.New(slog.NewJSONHandler(c, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
