package chatstream

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/stretchr/testify/require"
)

// onFinish receives the final usage and the time to first token — the two
// numbers the usage log needs and only the relay can see.
func TestServeChatStream_OnFinish(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(up.Close)

	resp, err := http.Get(up.URL) //nolint:gosec,noctx // test
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	rec := httptest.NewRecorder()
	var gotUsage models.Usage
	var gotTTFT time.Duration
	called := false

	ServeChatStream(rec, resp, "gpt", func(ttft time.Duration, usage models.Usage) {
		called = true
		gotTTFT, gotUsage = ttft, usage
	}, nil)

	require.True(t, called, "onFinish fires on the clean path")
	require.Equal(t, 9, gotUsage.InputTokens)
	require.Equal(t, 2, gotUsage.OutputTokens)
	require.Positive(t, gotTTFT)
}

// A broken stream (EOF before [DONE]) does not fire onFinish — the caller
// records the failure through its own error path.
func TestServeChatStream_OnFinishNotCalledOnBreak(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		// no [DONE] — the connection just ends
	}))
	t.Cleanup(up.Close)

	resp, err := http.Get(up.URL) //nolint:gosec,noctx // test
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	rec := httptest.NewRecorder()
	ServeChatStream(rec, resp, "gpt", func(time.Duration, models.Usage) {
		t.Fatal("onFinish must not fire on a broken stream")
	}, nil)
}

// nil onFinish is legal (a caller that does not care).
func TestServeChatStream_NilOnFinish(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(up.Close)

	resp, err := http.Get(up.URL) //nolint:gosec,noctx // test
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { ServeChatStream(rec, resp, "gpt", nil, nil) })
}

// A broken stream reports to onBreak — the usage entry must not record a
// clean 200 for a generation the client never finished receiving.
func TestServeChatStream_OnBreak(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		// no [DONE] — the connection just ends
	}))
	t.Cleanup(up.Close)

	resp, err := http.Get(up.URL) //nolint:gosec,noctx // test
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	rec := httptest.NewRecorder()
	broke := false
	ServeChatStream(rec, resp, "gpt", nil, func(_ string) { broke = true })

	require.True(t, broke)
}
