package anthropic_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
	"github.com/stretchr/testify/require"
)

// gatedWriter is a concurrency-safe ResponseRecorder stand-in whose first
// write is observable from the test goroutine (the stock recorder is not
// safe to poll while ServeMessages is writing from another goroutine).
type gatedWriter struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	header     http.Header
	firstWrite chan struct{}
	wrote      bool
}

func newGatedWriter() *gatedWriter {
	return &gatedWriter{header: http.Header{}, firstWrite: make(chan struct{})}
}

func (g *gatedWriter) Header() http.Header { return g.header }

func (g *gatedWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, err := g.buf.Write(p)
	if !g.wrote {
		g.wrote = true
		close(g.firstWrite)
	}

	return n, err
}

func (g *gatedWriter) WriteHeader(int) {}

func (g *gatedWriter) body() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.buf.String()
}

// A rectified STREAMING retry must relay its whole body: rectifyRetry's
// guard owns the retried request's context, and stopping it before the
// caller relays cancels that context — every read past the transport
// buffer then dies with "context canceled". The upstream holds the stream
// open in two gated chunks so the relay provably outlives rectifyRetry.
func TestRectifyRetry_StreamedRetryBodySurvivesGuardHandover(t *testing.T) {
	allowFirst := make(chan struct{})
	release := make(chan struct{})
	var hits atomic.Int32
	// every gate also opens on the request context and a hard time bound:
	// a dead test path must not leave the handler stuck (up.Close waits for
	// handlers — a gated wait alone deadlocks the cleanup after t.Fatal,
	// and a canceled client does not always tear the conn down either)
	waitGate := func(r *http.Request, ch <-chan struct{}) bool {
		select {
		case <-ch:
			return true
		case <-r.Context().Done():
			return false
		case <-time.After(10 * time.Second):
			return false
		}
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"messages.1.content.0.thinking: signature field required"}}`))

			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if !waitGate(r, allowFirst) {
			return
		}
		_, _ = w.Write([]byte("event: message_start\ndata: {\"x\":1}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if !waitGate(r, release) {
			return
		}
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"y\":2}\n\n"))
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	w := newGatedWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(thinkingHistory))
		tr.ServeMessages(w, req)
	}()

	// With the guard stopped inside rectifyRetry, the canceled context kills
	// the relay's first read and ServeMessages returns almost immediately.
	// Healthy path: the relay is parked on the upstream body (nothing
	// written yet), so done stays silent.
	select {
	case <-done:
		t.Fatal("streamed retry died before any body byte was relayed (guard canceled the retried request)")
	case <-time.After(300 * time.Millisecond):
	}

	close(allowFirst)
	select {
	case <-w.firstWrite:
	case <-time.After(2 * time.Second):
		t.Fatal("first SSE chunk never arrived")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay never finished")
	}

	require.EqualValues(t, 2, hits.Load())
	require.Contains(t, w.body(), "message_start")
	require.Contains(t, w.body(), "message_stop")
}

// The synthetic 502 body for a failed retried attempt must be valid JSON:
// transport error texts quote the URL ("Post \"http://…\": …"), and a raw
// quote spliced into the hand-built envelope breaks the client's parser.
// The watchdog timeout (shrunk here) produces exactly such an error.
func TestRectifyRetry_TransportErrorBodyIsValidJSON(t *testing.T) {
	readguard.FirstByte = 100 * time.Millisecond
	t.Cleanup(func() { readguard.FirstByte = 120 * time.Second })

	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"messages.1.content.0.thinking: signature field required"}}`))

			return
		}
		// second attempt: never answers — the watchdog cancels it. The time
		// bound keeps up.Close able to finish even though a canceled client
		// request does not reliably close the connection.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(thinkingHistory))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.True(t, json.Valid(rec.Body.Bytes()),
		"synthetic 502 body must be valid JSON, got: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "rectified retry failed")
	require.Contains(t, rec.Body.String(), "upstream first byte timeout")
}
