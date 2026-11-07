package anthropic_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
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
func TestAnthropic_HungUpstreamTimesOut(t *testing.T) {
	shortReadGuard(t)
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-x","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	started := time.Now()
	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "first byte timeout")
	require.Less(t, time.Since(started), time.Second, "must give up, not hang")
}
