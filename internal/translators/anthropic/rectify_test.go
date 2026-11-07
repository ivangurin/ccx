package anthropic_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
	"github.com/stretchr/testify/require"
)

const thinkingHistory = `{"model":"claude-x","max_tokens":50,"stream":true,
	"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"sig1"},{"type":"text","text":"answer"}]},
		{"role":"user","content":"go on"}
	]}`

// A signature-contract 400 is retried once with the thinking blocks stripped
// from the history — relay-compatible providers reject histories Claude Code
// itself accepts.
func TestRectifyRetry_StripsThinkingAndSucceeds(t *testing.T) {
	var requests [][]byte
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, data)
		mu.Unlock()
		if len(requests) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0.thinking: signature field required"}}`))

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}]}`))
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(thinkingHistory))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, requests, 2)
	require.Contains(t, string(requests[0]), `"thinking":"hmm"`)
	require.NotContains(t, string(requests[1]), `"thinking":"hmm"`, "retry body carries no thinking blocks")
	require.NotContains(t, string(requests[1]), `"signature"`)
}

// A budget_tokens floor 400 is retried with the budget clamped to the floor.
func TestRectifyRetry_ClampsThinkingBudget(t *testing.T) {
	var requests [][]byte
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, data)
		mu.Unlock()
		if len(requests) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"thinking.budget_tokens: budget_tokens must be at least 1024"}}`))

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m1","type":"message","role":"assistant","content":[]}`))
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	reqBody := `{"model":"claude-x","max_tokens":50,"thinking":{"type":"enabled","budget_tokens":100},"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, requests, 2)
	require.Contains(t, string(requests[1]), `"budget_tokens":1024`)
}

// An unrelated 400 is relayed as is — one attempt, no blind retries.
func TestRectifyRetry_Unrelated400SingleAttempt(t *testing.T) {
	hits := 0
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"max_tokens is required"}}`))
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-x","messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "max_tokens is required")
	require.Equal(t, 1, hits)
}

// A retried request that still fails relays the SECOND error (the one the
// rectified body produced), not the first.
func TestRectifyRetry_SecondFailureRelayed(t *testing.T) {
	var msgs []string
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		msgs = append(msgs, "")
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"still bad: signature"}}`))
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(thinkingHistory))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "still bad: signature")
}
