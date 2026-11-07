package anthropic_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
	"github.com/stretchr/testify/require"
)

// count_tokens is not a billable interaction — it must not write usage
// entries (the passthrough would double-book next to the other protocols,
// which answer it locally).
func TestCountTokensNotUsageLogged(t *testing.T) {
	capt := &usageCapture2{}
	old := usagelog.Default
	usagelog.Default = capt
	t.Cleanup(func() { usagelog.Default = old })

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Name: "relay", Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeCountTokens(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, capt.entries, "count_tokens writes no usage entries")
}

// A retry transport failure surfaces its real cause, not a bare constant.
func TestRectifyRetryTransportErrorHasCause(t *testing.T) {
	hits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 { // first attempt: the fixable thinking-contract 400
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"thinking: signature field required"}}`))

			return
		}
		panic(http.ErrAbortHandler) // the rectified retry dies mid-flight
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(thinkingHistory))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "rectified retry failed:")
	require.NotEqual(t,
		`{"type":"error","error":{"type":"api_error","message":"rectified retry failed"}}`,
		rec.Body.String(), "the real cause rides along")
}

// The rectified retry gets a FRESH deadline: the first attempt already spent
// most of the armed budget, and a fixable request must not die on the stale
// remainder.
func TestRectifyRetryGetsFreshDeadline(t *testing.T) {
	oldFirst, oldIdle, oldNon := readguard.FirstByte, readguard.Idle, readguard.NonStream
	readguard.FirstByte, readguard.Idle, readguard.NonStream = 10*time.Second, 10*time.Second, 150*time.Millisecond
	t.Cleanup(func() { readguard.FirstByte, readguard.Idle, readguard.NonStream = oldFirst, oldIdle, oldNon })

	hits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond) // both attempts are slow
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"thinking: signature field required"}}`))

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

	// 100ms + 100ms > the shared 150ms budget, but each attempt fits the
	// fresh 150ms — the re-armed retry completes
	require.Equal(t, http.StatusOK, rec.Code)
}

type usageCapture2 struct {
	entries []usagelog.Entry
}

func (c *usageCapture2) Record(e usagelog.Entry) {
	c.entries = append(c.entries, e)
}
