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
	oldFirst, oldIdle := readguard.FirstByte, readguard.Idle
	readguard.FirstByte, readguard.Idle = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { readguard.FirstByte, readguard.Idle = oldFirst, oldIdle })
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
