package openai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

type captureRecorder struct {
	mu      sync.Mutex
	entries []usagelog.Entry
}

func (c *captureRecorder) Record(e usagelog.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *captureRecorder) last(t *testing.T) usagelog.Entry {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.NotEmpty(t, c.entries)

	return c.entries[len(c.entries)-1]
}

func captureUsage(t *testing.T) *captureRecorder {
	t.Helper()
	capt := &captureRecorder{}
	old := usagelog.Default
	usagelog.Default = capt
	t.Cleanup(func() { usagelog.Default = old })

	return capt
}

// A streamed turn records tokens, TTFT and latency under the profile's name.
func TestUsageRecorded_StreamTurn(t *testing.T) {
	capt := captureUsage(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// a real first-token delay: headers flush immediately, the first
		// chunk lands 50ms later (localhost streams are sub-millisecond and
		// a 0ms TTFT would be a rounding artifact, not a missing signal)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(up.Close)

	tr := openai.New(&models.Profile{Name: "work-relay", Protocol: models.ProtocolOpenAI, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-6.1","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	e := capt.last(t)
	require.Equal(t, "work-relay", e.Provider)
	require.Equal(t, "openai", e.Protocol)
	require.Equal(t, "gpt-6.1", e.Model)
	require.True(t, e.Stream)
	require.Equal(t, 11, e.InputTokens)
	require.Equal(t, 3, e.OutputTokens)
	require.Positive(t, e.TTFTMS)
	require.Positive(t, e.LatencyMS)
	require.Equal(t, http.StatusOK, e.Status)
	require.WithinDuration(t, time.Now(), e.Ts, time.Minute)
}

// An upstream rejection records the upstream status, not a fake success.
func TestUsageRecorded_UpstreamError(t *testing.T) {
	capt := captureUsage(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	t.Cleanup(up.Close)

	tr := openai.New(&models.Profile{Name: "work-relay", Protocol: models.ProtocolOpenAI, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-6.1","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	e := capt.last(t)
	require.Equal(t, http.StatusTooManyRequests, e.Status)
	require.Zero(t, e.InputTokens)
}
