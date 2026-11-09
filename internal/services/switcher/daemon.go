package switcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
)

// Wait/escalation timings (vars: tests shorten them; the defaults bound the
// restart to ~45s worst case — graceful drain of long SSE, then signals).
var (
	portWait  = 10 * time.Second // /admin/stop → port closed
	termWait  = 2 * time.Second  // SIGTERM → port closed
	killWait  = 2 * time.Second  // SIGKILL → port closed
	drainWait = 35 * time.Second // port closed → process exit (Shutdown drains ≤30s + slack)
	pollInt   = 200 * time.Millisecond
)

// spawn is the detached-launch function (seam for tests).
var spawn = app.SpawnDetached

// SetWaitTimingsForTest overrides the wait timings (restored by the test's
// cleanup; tests are not parallel).
func SetWaitTimingsForTest(port, term, kill, drain, poll time.Duration) {
	portWait, termWait, killWait, drainWait, pollInt = port, term, kill, drain, poll
}

// SetSpawnForTest replaces the spawn function; ResetSpawnForTest restores it.
func SetSpawnForTest(fn func(*config.Config, ...string) error) { spawn = fn }

// ResetSpawnForTest restores the real spawn.
func ResetSpawnForTest() { spawn = app.SpawnDetached }

// HTTPDaemon — the Daemon implementation: restart = stop + detached spawn
// with launch params + health wait; stop = graceful /admin/stop with a
// signal escalation (a hung SSE stream can hold the graceful drain forever).
type HTTPDaemon struct {
	cfg *config.Config
}

// NewHTTPDaemon creates the daemon client.
func NewHTTPDaemon(cfg *config.Config) *HTTPDaemon {
	return &HTTPDaemon{cfg: cfg}
}

func baseURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// Restart stops any running daemon, waits out its drain, spawns a fresh one
// with the launch parameters (--provider/--port, plus --debug when the
// switcher read daemon.debug) and waits for /health — which answering means
// the translator passed Ready() (the daemon builds the route before
// listening).
func (d *HTTPDaemon) Restart(ctx context.Context, providerKey string, port int, debug bool) error {
	if err := d.Stop(ctx); err != nil {
		return err
	}
	if err := d.waitDrained(ctx); err != nil {
		return err
	}
	args := []string{"--provider", providerKey, "--port", strconv.Itoa(port)}
	if debug {
		args = append(args, "--debug")
	}
	if err := spawn(d.cfg, args...); err != nil {
		return fmt.Errorf("daemon: spawn: %w", err)
	}
	base := baseURL(port)
	if !app.WaitForHealth(ctx, base, d.cfg.DaemonStartTimeout) {
		return fmt.Errorf("daemon did not respond within %s (logs: %s/daemon-*.log)", d.cfg.DaemonStartTimeout, d.cfg.LogDir())
	}

	return nil
}

// waitDrained waits for the old daemon's process to be gone after its port
// closed. http.Server.Shutdown closes the listener at once but drains
// in-flight requests for up to 30s while holding daemon.lock — spawning
// before the exit would lose the lock race (the new daemon exits quietly)
// and fail the switch with the daemon down. At the deadline a still-living
// ccx process is hard-killed (it overran its own drain budget).
func (d *HTTPDaemon) waitDrained(ctx context.Context) error {
	pid, ok := d.readPid()
	if !ok {
		return nil // nothing identifiable to wait for (clean exit already, or a foreign server)
	}
	deadline := time.Now().Add(drainWait)
	for pidAlive(pid) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			if ownsCCX(pid) {
				_ = syscall.Kill(pid, syscall.SIGKILL) //nolint:gosec // our own overran daemon
			}

			return nil // best-effort: the spawn-side lock retry is the last line
		}
		time.Sleep(pollInt)
	}

	return nil
}

// Stop — best-effort shutdown of a running daemon: a no-op success if it is
// not running; otherwise POST /admin/stop, then escalate — a lingering
// listener would break the next spawn (port conflict), and a long SSE stream
// can hold the graceful drain indefinitely.
//
// OWNERSHIP: the port is a machine-global namespace, CCX_HOME is not. A
// daemon answering on the resolved port belongs to THIS home only when its
// daemon.pid names a live process; anything else is a foreign daemon
// (another home's — on a dev box, the live one on 4567 while tests run in
// their sandboxes) and is REFUSED, not stopped: test-run Stops killed live
// daemons and left Claude Code sessions on ECONNREFUSED for a whole day
// before the admin access log named the killer (verified live 2026-11-09).
// The refusal is an error so the caller decides: a direct-profile switch
// prints a note and stays committed, Restart fails fast instead of losing
// the bind race cryptically.
func (d *HTTPDaemon) Stop(ctx context.Context) error {
	port := config.ResolveDaemonPort(d.cfg, 0)
	base := baseURL(port)
	if !app.HealthCheck(base, time.Second) {
		return nil
	}
	// ownsCCX, not pidAlive: signal-0 counts ANY live process as the owner
	// (a foreign process even passes via EPERM), so pidAlive alone lets a
	// stale pidfile whose pid was REUSED by an unrelated process pass the
	// gate — /admin/stop would then reach a foreign daemon, the very
	// incident class this gate exists for. A SIGKILLed daemon leaves its
	// pidfile behind (its defer never runs), making stale pidfiles routine.
	if pid, ok := d.readPid(); !ok || !ownsCCX(pid) {
		return fmt.Errorf(
			"refusing to stop the daemon on port %d: it is not owned by %s (no live pid in daemon.pid) — a foreign CCX_HOME's daemon",
			port, d.cfg.Home,
		)
	}
	d.postStop(ctx, base)

	// graceful: the port closes after the drain
	if d.waitPortClosed(ctx, base, portWait) {
		return nil
	}
	// escalation: signals by the pidfile (a missing/garbage pidfile is
	// best-effort — nothing to signal, fall through to the final wait). The
	// pid's identity is verified first: a stale pidfile whose pid was reused
	// must not get an innocent process killed.
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if pid, ok := d.readPid(); ok && ownsCCX(pid) {
			_ = syscall.Kill(pid, sig) //nolint:gosec // our own daemon process
		}
		wait := termWait
		if sig == syscall.SIGKILL {
			wait = killWait
		}
		if d.waitPortClosed(ctx, base, wait) {
			return nil
		}
	}

	return fmt.Errorf("the daemon is still responding (logs: %s/daemon-*.log)", d.cfg.LogDir())
}

// postStop fires the graceful-shutdown request; the status is ignored — the
// port wait and the signal escalation are the real enforcement.
func (d *HTTPDaemon) postStop(ctx context.Context, base string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/admin/stop", nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Ccx", "1")                                  // the admin marker (browser drive-by defense)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req) //nolint:gosec // local address
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (d *HTTPDaemon) waitPortClosed(ctx context.Context, base string, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if !app.HealthCheck(base, time.Second) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollInt):
		}
	}
}

// readPid parses daemon.pid; any failure means "nothing to signal".
func (d *HTTPDaemon) readPid() (int, bool) {
	raw, err := os.ReadFile(filepath.Join(d.cfg.Home, "daemon.pid")) //nolint:gosec // fixed path
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}

	return pid, true
}

// pidAlive — signal 0 probe: nil or EPERM means the pid exists (EPERM:
// another user's process), ESRCH means it is gone.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)

	return err == nil || errors.Is(err, syscall.EPERM)
}

// ownsCCX — the pid still runs a binary with our own name (ps comm). The
// signal ladder must never fire on a reused pid from a stale pidfile.
func ownsCCX(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output() //nolint:gosec // read-only query
	if err != nil {
		return false
	}
	comm := strings.TrimSpace(string(out))
	base := filepath.Base(os.Args[0])

	return comm == base || strings.HasSuffix(comm, string(filepath.Separator)+base)
}
