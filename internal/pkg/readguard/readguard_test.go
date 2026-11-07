package readguard

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// hangServer is an upstream that accepts the request and never answers —
// not even the response headers.
func hangServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
}

// headerHangServer sends the response headers and then hangs before the
// first body byte — the shape of an SSE upstream that never starts generating.
func headerHangServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(2 * time.Second)
	}))
}

// stallServer sends one chunk, then stalls longer than the idle deadline.
func stallServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first-chunk"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte("late-chunk"))
	}))
}

// trickleServer sends a chunk every 20ms — steady, well under the idle deadline.
func trickleServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for range 5 {
			_, _ = w.Write([]byte("chunk\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
}

// guardedGet performs a GET whose body is deadline-guarded, mirroring the
// translators' usage: Arm before the request, Body after the response.
func guardedGet(t *testing.T, url string, firstByte, idle time.Duration) *http.Response {
	t.Helper()

	gctx, g := Arm(context.Background(), firstByte, idle)
	t.Cleanup(g.Stop)
	req, err := http.NewRequestWithContext(gctx, http.MethodGet, url, nil) //nolint:noctx // guarded ctx
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body = g.Body(resp.Body) //nolint:bodyclose // closed by the caller

	return resp
}

func TestFirstByteTimeoutBeforeHeaders(t *testing.T) {
	srv := hangServer()
	defer srv.Close()

	gctx, g := Arm(context.Background(), 50*time.Millisecond, 50*time.Millisecond)
	defer g.Stop()
	req, err := http.NewRequestWithContext(gctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	require.Contains(t, g.Err(err).Error(), "upstream first byte timeout after 50ms")
}

func TestFirstByteTimeoutOnBody(t *testing.T) {
	srv := headerHangServer()
	defer srv.Close()

	resp := guardedGet(t, srv.URL, 50*time.Millisecond, 50*time.Millisecond)
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 16)
	_, err := resp.Body.Read(buf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "upstream first byte timeout after 50ms")
}

func TestIdleTimeout(t *testing.T) {
	srv := stallServer()
	defer srv.Close()

	resp := guardedGet(t, srv.URL, 2*time.Second, 50*time.Millisecond)
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 64)
	n, err := io.ReadFull(resp.Body, buf[:len("first-chunk")])
	require.NoError(t, err)
	require.Equal(t, "first-chunk", string(buf[:n]))

	// the stall exceeds the idle deadline — the next read must give up
	_, err = resp.Body.Read(buf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "upstream idle timeout after 50ms")
}

func TestSteadyStreamPasses(t *testing.T) {
	srv := trickleServer()
	defer srv.Close()

	resp := guardedGet(t, srv.URL, 2*time.Second, 100*time.Millisecond)
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("chunk\n", 5), string(data))
}

func TestEOFBeforeDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("done"))
	}))
	defer srv.Close()

	resp := guardedGet(t, srv.URL, 2*time.Second, 2*time.Second)
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "done", string(data))
	require.NoError(t, resp.Body.Close())
}

func TestStopReleasesWatchdog(t *testing.T) {
	// Stop without any response (an upstream call that failed before a body)
	// must cancel the armed context, must not poison the parent, and a second
	// Arm on the same parent must work.
	parent := context.Background()
	gctx, g := Arm(parent, time.Hour, time.Hour)
	g.Stop()

	select {
	case <-gctx.Done():
		require.ErrorIs(t, gctx.Err(), context.Canceled)
	default:
		t.Fatal("Stop must cancel the armed context")
	}
	require.NoError(t, parent.Err())

	gctx2, g2 := Arm(parent, time.Hour, time.Hour)
	require.NoError(t, gctx2.Err())
	g2.Stop()
}
