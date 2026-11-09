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

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/switcher"
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

	p := &models.Providers{Daemon: &models.Daemon{Port: port}}
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
	switcher.SetWaitTimingsForTest(200*time.Millisecond, 200*time.Millisecond, 200*time.Millisecond, 400*time.Millisecond, 50*time.Millisecond)
}

func TestHTTPDaemon_StopNotRunning(t *testing.T) {
	f := newFakeAdminDaemon(t, true)
	f.srv.Close() // nothing is listening anymore

	d := switcher.NewHTTPDaemon(f.config(t))
	require.NoError(t, d.Stop(context.Background()))
}

// The regression pin for the 2026-11-09 live incident: the port is a
// machine-global namespace, CCX_HOME is not. A daemon answering on the
// resolved port but with NO live pid in this home's daemon.pid is FOREIGN
// (another home's — on a dev box the live one on 4567 while tests run in
// their sandboxes). Stop used to admin-stop it sight unseen: every
// `go test ./...` run killed the user's daemon and left Claude Code
// sessions on ECONNREFUSED for a whole day (verified live — the admin
// access log named the killer).
func TestHTTPDaemon_StopRefusesForeignDaemon(t *testing.T) {
	f := newFakeAdminDaemon(t, true) // would close on stop — must never get it

	d := switcher.NewHTTPDaemon(f.config(t)) // no daemon.pid in this home
	err := d.Stop(context.Background())

	require.ErrorContains(t, err, "foreign")
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Nil(t, f.stopReq, "/admin/stop must not be sent to a foreign daemon")
}

// ownDaemonPid — a live pidfile naming a process the ownership gate ACCEPTS:
// since the gate checks ownsCCX (ps comm == our binary — pid reuse by any
// unrelated process must not pass), the pidfile must name a selfChild, not
// an arbitrary /bin/sleep (that shape is covered by the foreign-refusal
// tests).
func ownDaemonPid(t *testing.T, cfg *config.Config) {
	t.Helper()

	sleep := selfChild(t)
	require.NoError(t, os.MkdirAll(cfg.Home, 0o750))
	require.NoError(t, os.WriteFile( //nolint:gosec // fixed path
		filepath.Join(cfg.Home, "daemon.pid"), []byte(strconv.Itoa(sleep.Process.Pid)), 0o600,
	))
}

func TestHTTPDaemon_StopPostsAndWaits(t *testing.T) {
	f := newFakeAdminDaemon(t, true)
	cfg := f.config(t)
	ownDaemonPid(t, cfg) // this home owns the daemon — the stop may proceed

	d := switcher.NewHTTPDaemon(cfg)
	require.NoError(t, d.Stop(context.Background())) // returns after the port closes

	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotNil(t, f.stopReq, "/admin/stop was not called")
	require.Equal(t, http.MethodPost, f.stopReq.Method)
}

func TestHTTPDaemon_StopStillResponding(t *testing.T) {
	f := newFakeAdminDaemon(t, false) // ignores the stop request
	shortenWaits()
	cfg := f.config(t)
	ownDaemonPid(t, cfg) // owned: the ladder runs (and rightly spares the sleep pid)

	// short deadline: the wait loop gives up via ctx instead of hanging
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	d := switcher.NewHTTPDaemon(cfg)
	err := d.Stop(ctx)
	require.ErrorContains(t, err, "still responding")
}

// The old daemon ignores /admin/stop (a long SSE hangs the graceful drain):
// Stop escalates to SIGTERM/SIGKILL by the pidfile and wins.
func TestHTTPDaemon_StopEscalatesToSignalWhenPortStaysOpen(t *testing.T) {
	shortenWaits()
	f := newFakeAdminDaemon(t, false) // port stays open after /admin/stop

	cfg := f.config(t)
	// a real child in place of the daemon process (running OUR binary —
	// the identity check must let the signal ladder through); its death
	// closes the port
	procDone := make(chan struct{})
	sleep := selfChild(t)
	go func() {
		_ = sleep.Wait()
		close(procDone)
	}()
	require.NoError(t, os.MkdirAll(cfg.Home, 0o750))
	require.NoError(t, os.WriteFile( //nolint:gosec // fixed path
		filepath.Join(cfg.Home, "daemon.pid"),
		[]byte(strconv.Itoa(sleep.Process.Pid)), 0o600,
	))
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

// A missing or garbage pidfile is best-effort, never an error in itself
// (with an OPEN port both now mean "not provably ours" — the foreign-daemon
// refusal — which is an error the caller can act on, never a crash).
func TestHTTPDaemon_StopMissingOrGarbagePidfile(t *testing.T) {
	t.Run("closed port, no pidfile", func(t *testing.T) {
		f := newFakeAdminDaemon(t, true)
		f.srv.Close() // nothing is listening — the health gate answers first
		cfg := f.config(t)

		d := switcher.NewHTTPDaemon(cfg)
		require.NoError(t, d.Stop(context.Background()))
	})

	t.Run("open port, garbage pidfile", func(t *testing.T) {
		shortenWaits()
		f := newFakeAdminDaemon(t, false) // port stays open
		cfg := f.config(t)
		require.NoError(t, os.MkdirAll(cfg.Home, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(cfg.Home, "daemon.pid"), []byte("garbage"), 0o600)) //nolint:gosec // test sandbox

		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		d := switcher.NewHTTPDaemon(cfg)
		require.NotPanics(t, func() {
			_ = d.Stop(ctx) // foreign-daemon refusal — the pidfile was unusable, not a crash
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
// spawn with --provider/--port (--debug when the launch carries it), wait
// for health on the new one — in order.
func TestHTTPDaemon_RestartStopsThenSpawnsThenWaitsHealth(t *testing.T) {
	for name, debug := range map[string]bool{"plain": false, "debug": true} {
		t.Run(name, func(t *testing.T) {
			shortenWaits()
			f := newFakeAdminDaemon(t, true) // the OLD daemon: closes on /admin/stop

			cfg := f.config(t)
			ownDaemonPid(t, cfg) // this home owns the old daemon — Restart's stop phase proceeds
			oldPort := serverPort(t, f.srv)
			cfg.DaemonStartTimeout = 2 * time.Second

			// spawn seam: remember the args and start the NEW fake daemon on
			// the same port (free after the stop) — /health answers from then on.
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
			require.NoError(t, d.Restart(context.Background(), "work", oldPort, debug))

			events := f.snapshot()
			require.Equal(t, []string{"stop", "spawn", "health-up"}, events)
			spawnMu.Lock()
			defer spawnMu.Unlock()
			want := []string{"--provider", "work", "--port", strconv.Itoa(oldPort)}
			if debug {
				want = append(want, "--debug")
			}
			require.Equal(t, want, spawnArgs)
		})
	}
}

// The spawned daemon never becomes healthy — Restart fails with the log hint.
func TestHTTPDaemon_RestartHealthTimeout(t *testing.T) {
	shortenWaits()
	f := newFakeAdminDaemon(t, true)
	cfg := f.config(t)
	ownDaemonPid(t, cfg) // owned old daemon: the stop phase proceeds, the health wait is what fails
	cfg.DaemonStartTimeout = 300 * time.Millisecond

	switcher.SetSpawnForTest(func(_ *config.Config, _ ...string) error { return nil }) // spawns nothing
	t.Cleanup(switcher.ResetSpawnForTest)

	d := switcher.NewHTTPDaemon(cfg)
	err := d.Restart(context.Background(), "work", serverPort(t, f.srv), false)
	require.ErrorContains(t, err, "daemon-*.log")
}

// serverPort — the TCP port of a test server (errcheck-safe assertion).
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr, ok := srv.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}

// Real drain scenario: /admin/stop closes the listener at once but the old
// daemon lives on (Shutdown drains in-flight SSE up to 30s holding
// daemon.lock). Restart must wait out the drain — or hard-kill at the
// deadline — instead of spawning into the lock race.
func TestHTTPDaemon_RestartWaitsForDrainingOldDaemon(t *testing.T) {
	shortenWaits()
	f := newFakeAdminDaemon(t, true) // port closes on /admin/stop, "process" lives on

	cfg := f.config(t)
	oldPort := serverPort(t, f.srv)
	cfg.DaemonStartTimeout = 2 * time.Second
	// the child runs our own binary: the identity check must let the
	// hard-kill through at the drain deadline
	child := selfChild(t)
	require.NoError(t, os.MkdirAll(cfg.Home, 0o750))
	require.NoError(t, os.WriteFile( //nolint:gosec // fixed path
		filepath.Join(cfg.Home, "daemon.pid"), []byte(strconv.Itoa(child.Process.Pid)), 0o600,
	))

	// the spawn seam brings up the NEW fake daemon on the freed port
	switcher.SetSpawnForTest(func(_ *config.Config, _ ...string) error {
		lis, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(oldPort))
		if err != nil {
			return err
		}
		newFake := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		newFake.Listener = lis
		newFake.Start()
		t.Cleanup(newFake.Close)

		return nil
	})
	t.Cleanup(switcher.ResetSpawnForTest)

	d := switcher.NewHTTPDaemon(cfg)
	require.NoError(t, d.Restart(context.Background(), "work", oldPort, false))
	select {
	case <-childDone(t, child):
	case <-time.After(3 * time.Second):
		t.Fatal("the draining daemon was not killed at the drain deadline")
	}
}

// A pid that no longer belongs to ccx must never be signaled, even when the
// port stays open: the ladder only escalates against our own process.
func TestHTTPDaemon_StopSkipsSignalForForeignPid(t *testing.T) {
	shortenWaits()
	f := newFakeAdminDaemon(t, false) // port stays open

	cfg := f.config(t)
	foreign := exec.Command("/bin/sleep", "30") //nolint:gosec // system binary, foreign name
	require.NoError(t, foreign.Start())
	t.Cleanup(func() { _ = foreign.Process.Kill() })
	require.NoError(t, os.MkdirAll(cfg.Home, 0o750))
	require.NoError(t, os.WriteFile( //nolint:gosec // fixed path
		filepath.Join(cfg.Home, "daemon.pid"), []byte(strconv.Itoa(foreign.Process.Pid)), 0o600,
	))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d := switcher.NewHTTPDaemon(cfg)
	// the GATE already refuses a live-but-foreign pid (ownsCCX, not the bare
	// signal-0 pidAlive): a reused pid must not pass the ownership check any
	// more than it must be signaled by the ladder below
	require.ErrorContains(t, d.Stop(ctx), "refusing to stop")

	// alive = no death event within a grace window (a zombie would also
	// answer kill(pid,0), so the assertion must observe the process, not the pid)
	foreignDone := childDone(t, foreign)
	select {
	case <-foreignDone:
		t.Fatal("a foreign pid must survive the escalation")
	case <-time.After(500 * time.Millisecond):
	}
}

func childDone(t *testing.T, cmd *exec.Cmd) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	return done
}

// selfChild — a live process whose ps comm IS our own binary's name (the
// identity check compares against it; copying /bin/sleep is not an option:
// macOS kills signature-broken copies on exec). The child re-runs this test
// binary's helper case and just sleeps.
func selfChild(t *testing.T) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.run=^TestHelperSleepProcess$", "--", "30") //nolint:gosec // our own test binary
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	return cmd
}

// TestHelperSleepProcess is the selfChild payload (never runs as a real test:
// the guard returns unless the child env is set).
func TestHelperSleepProcess(_ *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}
