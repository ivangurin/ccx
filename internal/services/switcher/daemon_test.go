package switcher_test

import (
	"context"
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

	p := &models.Providers{Version: 2, Daemon: models.Daemon{Port: port, AuthToken: "tok"}}
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
