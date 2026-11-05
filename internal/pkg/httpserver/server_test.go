package httpserver_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/httpserver"
	"github.com/stretchr/testify/require"
)

func newEcho() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "pong")
	})

	return mux
}

func TestServer_ServesRequests(t *testing.T) {
	srv, err := httpserver.New("127.0.0.1:0", newEcho())
	require.NoError(t, err)

	go func() { _ = srv.Start() }()
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + srv.Addr() + "/ping") //nolint:gosec // local test address
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)

		return resp.StatusCode == http.StatusOK && string(body) == "pong"
	}, 5*time.Second, 50*time.Millisecond, "server did not start serving")
}

func TestServer_PortAssigned(t *testing.T) {
	srv, err := httpserver.New("127.0.0.1:0", newEcho())
	require.NoError(t, err)
	require.NotEmpty(t, srv.Addr())
}

func TestServer_StartReturnsAfterStop(t *testing.T) {
	srv, err := httpserver.New("127.0.0.1:0", newEcho())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- srv.Start() }()

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + srv.Addr() + "/ping") //nolint:gosec // local test address
		if err != nil {
			return false
		}
		_ = resp.Body.Close()

		return true
	}, 5*time.Second, 50*time.Millisecond)

	require.NoError(t, srv.Stop(context.Background()))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

func TestServer_RefusesAfterStop(t *testing.T) {
	srv, err := httpserver.New("127.0.0.1:0", newEcho())
	require.NoError(t, err)

	go func() { _ = srv.Start() }()
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + srv.Addr() + "/ping") //nolint:gosec // local test address
		if err != nil {
			return false
		}
		_ = resp.Body.Close()

		return true
	}, 5*time.Second, 50*time.Millisecond)

	require.NoError(t, srv.Stop(context.Background()))

	resp, err := http.Get("http://" + srv.Addr() + "/ping") //nolint:gosec // local test address
	if err == nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err)
}
