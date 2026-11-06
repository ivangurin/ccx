package switcher_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
// request; onProcDeath closes the listener when the pidfile process dies
// (wires "signal killed it" → "port closed" for the escalation test).
type fakeAdminDaemon struct {
	srv     *httptest.Server
	mu      sync.Mutex
	stopReq *http.Request
	events  []string
}

func newFakeAdminDaemon(t *testing.T, closeOnStop bool) *fakeAdminDaemon {
	t.Helper()
	f := &fakeAdminDaemon{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)

			return
		}
		if r.URL.Path == "/admin/stop" {
			f.mu.Lock()
			f.stopReq = r.Clone(r.Context())
			f.events = append(f.events, "stop")
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if closeOnStop {
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

	p := &models.Providers{Daemon: models.Daemon{Port: port}}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	return sp.Config
}

func (f *fakeAdminDaemon) record(ev string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeAdminDaemon) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.events...)
}

func shortenWaits() {
	switcher.SetWaitTimingsForTest(200*time.Millisecond, 200*time.Millisecond, 200*time.Millisecond, 50*time.Millisecond)
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
}

func TestHTTPDaemon_StopStillResponding(t *testing.T) {
	f := newFakeAdminDaemon(t, false) // ignores the stop request
	shortenWaits()

	// short deadline: the wait loop gives up via ctx instead of hanging
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	d := switcher.NewHTTPDaemon(f.config(t))
	err := d.Stop(ctx)
	require.ErrorContains(t, err, "still responding")
}

// The old daemon ignores /admin/stop (a long SSE hangs the graceful drain):
// Stop escalates to SIGTERM/SIGKILL by the pidfile and wins.
func TestHTTPDaemon_StopEscalatesToSignalWhenPortStaysOpen(t *testing.T) {
	shortenWaits()
	f := newFakeAdminDaemon(t, false) // port stays open after /admin/stop

	cfg := f.config(t)
	// a real child in place of the daemon process; its death closes the port
	procDone := make(chan struct{})
	sleep := exec.Command("sleep", "30") //nolint:gosec // test child
	require.NoError(t, sleep.Start())
	t.Cleanup(func() { _ = sleep.Process.Kill() })
	go func() {
		_ = sleep.Wait()
		close(procDone)
	}()
	require.NoError(t, os.MkdirAll(cfg.CCMSDir(), 0o750))
	require.NoError(t, os.WriteFile( //nolint:gosec // fixed path
		filepath.Join(cfg.CCMSDir(), "daemon.pid"),
		[]byte(strconv.Itoa(sleep.Process.Pid)), 0o600))
	// the fake daemon's handler watches the child: once it is dead, the port closes
	f.srv.Config.Handler = watchingHandler(f, procDone)

	d := switcher.NewHTTPDaemon(cfg)
	require.NoError(t, d.Stop(context.Background()))
	select {
	case <-procDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the pidfile process survived the SIGTERM/SIGKILL escalation")
	}
}

// A missing or garbage pidfile is best-effort, never an error in itself.
func TestHTTPDaemon_StopMissingOrGarbagePidfile(t *testing.T) {
	t.Run("closed port, no pidfile", func(t *testing.T) {
		f := newFakeAdminDaemon(t, true)
		cfg := f.config(t)

		d := switcher.NewHTTPDaemon(cfg)
		require.NoError(t, d.Stop(context.Background()))
	})

	t.Run("open port, garbage pidfile", func(t *testing.T) {
		shortenWaits()
		f := newFakeAdminDaemon(t, false) // port stays open
		cfg := f.config(t)
		require.NoError(t, os.MkdirAll(cfg.CCMSDir(), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(cfg.CCMSDir(), "daemon.pid"), []byte("garbage"), 0o600)) //nolint:gosec // test sandbox

		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		d := switcher.NewHTTPDaemon(cfg)
		require.NotPanics(t, func() {
			_ = d.Stop(ctx) // "still responding" — the pidfile was unusable, not a crash
		})
	})
}

// watchingHandler: the fake daemon surface; once procDone closes, the listener
// closes too (the next health poll observes a closed port).
func watchingHandler(f *fakeAdminDaemon, procDone <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-procDone:
			go f.srv.Close()
		default:
		}
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)

			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

// Restart against a fake old daemon + injectable spawn: stop the old one,
// spawn with --provider/--port, wait for health on the new one — in order.
func TestHTTPDaemon_RestartStopsThenSpawnsThenWaitsHealth(t *testing.T) {
	shortenWaits()
	f := newFakeAdminDaemon(t, true) // the OLD daemon: closes on /admin/stop

	cfg := f.config(t)
	oldPort := serverPort(t, f.srv)
	cfg.DaemonStartTimeout = 2 * time.Second

	// spawn seam: remember the args and start the NEW fake daemon on the same
	// port (free after the stop) — /health answers from then on.
	var spawnArgs []string
	var spawnMu sync.Mutex
	switcher.SetSpawnForTest(func(_ *config.Config, args ...string) error {
		spawnMu.Lock()
		spawnArgs = append([]string(nil), args...)
		spawnMu.Unlock()
		f.record("spawn")
		lis, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(oldPort))
		if err != nil {
			return err // the old daemon still holds the port — ordering broken
		}
		newFake := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		newFake.Listener = lis
		newFake.Start()
		t.Cleanup(newFake.Close)
		f.record("health-up")

		return nil
	})
	t.Cleanup(switcher.ResetSpawnForTest)

	d := switcher.NewHTTPDaemon(cfg)
	require.NoError(t, d.Restart(context.Background(), "work", oldPort))

	events := f.snapshot()
	require.Equal(t, []string{"stop", "spawn", "health-up"}, events)
	spawnMu.Lock()
	defer spawnMu.Unlock()
	require.Equal(t, []string{"--provider", "work", "--port", strconv.Itoa(oldPort)}, spawnArgs)
}

// The spawned daemon never becomes healthy — Restart fails with the log hint.
func TestHTTPDaemon_RestartHealthTimeout(t *testing.T) {
	shortenWaits()
	f := newFakeAdminDaemon(t, true)
	cfg := f.config(t)
	cfg.DaemonStartTimeout = 300 * time.Millisecond

	switcher.SetSpawnForTest(func(_ *config.Config, _ ...string) error { return nil }) // spawns nothing
	t.Cleanup(switcher.ResetSpawnForTest)

	d := switcher.NewHTTPDaemon(cfg)
	err := d.Restart(context.Background(), "work", serverPort(t, f.srv))
	require.ErrorContains(t, err, "daemon.log")
}

// serverPort — the TCP port of a test server (errcheck-safe assertion).
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr, ok := srv.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}
