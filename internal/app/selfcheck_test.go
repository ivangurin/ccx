package app_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/stretchr/testify/require"
)

// syncBuffer — a mutex-guarded bytes.Buffer: selfCheck writes from its own
// goroutine while the test reads, and a plain bytes.Buffer races (caught by
// -race on this very test).
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

// The self-check's whole point: a dead listener while the process is alive
// must produce a log line (the 2026-11-09 forensics had no such signal).
// A live listener must stay silent.
func TestSelfCheck(t *testing.T) {
	reset := app.SetSelfCheckIntervalForTest(50 * time.Millisecond)
	t.Cleanup(reset)

	t.Run("listener down logs a warn", func(t *testing.T) {
		var buf syncBuffer
		log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		done := make(chan struct{})
		go func() {
			app.SelfCheckForTest(log, "http://127.0.0.1:1", done)
		}()

		require.Eventually(t, func() bool {
			return bytes.Contains([]byte(buf.String()), []byte("self-check: listener not answering"))
		}, 2*time.Second, 20*time.Millisecond)
		close(done)
	})

	t.Run("listener up stays silent", func(t *testing.T) {
		var buf syncBuffer
		log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		done := make(chan struct{})
		go func() {
			app.SelfCheckForTest(log, upServer(t), done)
		}()

		time.Sleep(200 * time.Millisecond)
		close(done)
		require.NotContains(t, buf.String(), "self-check")
	})
}

// upServer — an answering /health on an ephemeral port.
func upServer(t *testing.T) string {
	t.Helper()

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(up.Close)

	return up.URL
}
