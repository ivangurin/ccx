package switcher_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/ivangurin/ccms/internal/services/switcher"
	"github.com/stretchr/testify/require"
)

// fakeAdminDaemon — a stand-in daemon: /health OK, /admin/stop records the
// request and closes the listener when asked to.
type fakeAdminDaemon struct {
	srv     *httptest.Server
	mu      sync.Mutex
	stopReq *http.Request
	closeOK bool
}

func newFakeAdminDaemon(t *testing.T, closeOnStop bool) *fakeAdminDaemon {
	t.Helper()
	f := &fakeAdminDaemon{closeOK: closeOnStop}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)

			return
		}
		if r.URL.Path == "/admin/stop" {
			f.mu.Lock()
			f.stopReq = r.Clone(r.Context())
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if f.closeOK {
				go f.srv.Close() // async: Close waits for outstanding requests
			}

			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(f.srv.Close)

	return f
}

// config — providers.json with daemon.port pointing at the fake server.
func (f *fakeAdminDaemon) config(t *testing.T) *config.Config {
	t.Helper()
	sp := suite_provider.NewProvider(t)
	u, err := url.Parse(f.srv.URL) // http://127.0.0.1:PORT
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	p := &models.Providers{Version: 3, Daemon: models.Daemon{Port: port, AuthToken: "tok"}}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	return sp.Config
}

func TestHTTPDaemon_StopNotRunning(t *testing.T) {
	f := newFakeAdminDaemon(t, true)
	f.srv.Close() // nothing is listening anymore

	d := switcher.NewHTTPDaemon(f.config(t))
	require.NoError(t, d.Stop(context.Background()))
}

func TestHTTPDaemon_StopPostsAndWaits(t *testing.T) {
	f := newFakeAdminDaemon(t, true)

	d := switcher.NewHTTPDaemon(f.config(t))
	require.NoError(t, d.Stop(context.Background())) // returns after the port closes

	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotNil(t, f.stopReq, "/admin/stop was not called")
	require.Equal(t, http.MethodPost, f.stopReq.Method)
	require.Equal(t, "Bearer tok", f.stopReq.Header.Get("Authorization"))
}

func TestHTTPDaemon_StopStillResponding(t *testing.T) {
	f := newFakeAdminDaemon(t, false) // ignores the stop request

	// short deadline: the wait loop gives up via ctx instead of hanging
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	d := switcher.NewHTTPDaemon(f.config(t))
	err := d.Stop(ctx)
	require.ErrorContains(t, err, "still responding")
}

// Ensure/Use against a live fake daemon: an alive /health means no spawn,
// /admin/use relays the profile/model pair with the daemon token.
func TestHTTPDaemon_EnsureAndUse_LiveDaemon(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/admin/use":
			raw, _ := io.ReadAll(r.Body)
			gotBody = string(raw)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	p := &models.Providers{Version: 3, Daemon: models.Daemon{Port: 4567, AuthToken: "tok"}}
	p.Profiles.Set("x", &models.Profile{Name: "X", Protocol: models.ProtocolOpenAI, BaseURL: "https://gw"})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	sp.Config.DaemonPort = serverPort(t, srv) // the fake daemon's port

	d := switcher.NewHTTPDaemon(sp.Config)
	require.NoError(t, d.Ensure(context.Background()))
	require.NoError(t, d.Use(context.Background(), "x", "m"))
	require.Equal(t, "Bearer tok", gotAuth)
	require.JSONEq(t, `{"profile":"x","model":"m"}`, gotBody)
}

// Use surfaces the daemon's refusal (a not-ready translator) as an error.
func TestHTTPDaemon_UseRefused(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"not ready"}`))
	}))
	t.Cleanup(srv.Close)

	p := &models.Providers{Version: 3, Daemon: models.Daemon{Port: 4567, AuthToken: "tok"}}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	sp.Config.DaemonPort = serverPort(t, srv)

	d := switcher.NewHTTPDaemon(sp.Config)
	err := d.Use(context.Background(), "x", "m")
	require.ErrorContains(t, err, "not ready")
}

// serverPort — the TCP port of a test server (errcheck-safe assertion).
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr, ok := srv.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}
