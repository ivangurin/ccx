package openai_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/stretchr/testify/require"
)

// shortReadGuard shrinks the translator read deadlines for the test and
// restores them afterwards.
func shortReadGuard(t *testing.T) {
	t.Helper()
	oldFirst, oldIdle, oldNon := readguard.FirstByte, readguard.Idle, readguard.NonStream
	readguard.FirstByte, readguard.Idle, readguard.NonStream = 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { readguard.FirstByte, readguard.Idle, readguard.NonStream = oldFirst, oldIdle, oldNon })
}

// A hung upstream (nothing ever sent) must answer 502 quickly instead of
// leaving the client request open forever.
func TestServeMessages_HungUpstreamTimesOut(t *testing.T) {
	shortReadGuard(t)
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(up.Close)

	tr := newTranslator(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	started := time.Now()
	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "first byte timeout")
	require.Less(t, time.Since(started), time.Second, "must give up, not hang")
}

// A non-stream response's first body byte arrives only when the generation
// is COMPLETE — the 120s first-token patience must not cap it; the non-stream
// arm uses the whole-generation budget instead.
func TestServeMessages_NonStreamSlowGenerationOK(t *testing.T) {
	oldFirst, oldIdle, oldNon := readguard.FirstByte, readguard.Idle, readguard.NonStream
	readguard.FirstByte, readguard.Idle, readguard.NonStream = 100*time.Millisecond, time.Hour, 5*time.Second
	t.Cleanup(func() { readguard.FirstByte, readguard.Idle, readguard.NonStream = oldFirst, oldIdle, oldNon })

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond) // > first-byte, well under the generation budget
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"slow but fine"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(up.Close)

	tr := newTranslator(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "slow but fine")
}
