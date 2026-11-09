// Package app — daemon wiring for ccx serve: config → logger → lock →
// route (Build from --provider, Ready before listening) → http → signals →
// graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ivangurin/ccx/internal/api/handlers"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/pkg/closer"
	"github.com/ivangurin/ccx/internal/pkg/flock"
	"github.com/ivangurin/ccx/internal/pkg/httpserver"
	"github.com/ivangurin/ccx/internal/pkg/reqdump"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/services/router"
)

// Version — daemon version (ldflags).
var Version = "dev"

// ServeOptions — the daemon's launch parameters (ccx serve flags).
type ServeOptions struct {
	// Provider is the providers.json key the route is built from (required).
	Provider string
	// Port is the listen port; 0 = resolve (CCX_PORT > 4567 — providers.json
	// daemon.port is switcher-owned and never read here; the switcher always
	// passes an explicit --port).
	Port int
	// Debug raises the daemon log level to debug (format stays env-driven)
	// and enables request dumps (wired in cli; Run itself only uses it for
	// the dump gate and the self-check).
	Debug bool
}

// App — the daemon.
type App struct {
	cfg *config.Config
	log *slog.Logger
}

// New creates an App.
func New(cfg *config.Config, log *slog.Logger) *App {
	return &App{cfg: cfg, log: log}
}

// Run blocks until a signal/ctx cancel (or /admin/stop). The route is built
// from opts BEFORE listening: /health answering means the translator passed
// Ready(); a startup failure exits non-zero with the reason in daemon-*.log.
// A double start is prevented by flock daemon.lock; the lock is retried for
// a few seconds first — a restarting daemon may still hold it during its
// graceful shutdown (the spawner waits for the port to close, not the exit).
// A lock that is still held after the retry is an ERROR, not a quiet exit:
// runServe turns it into a non-zero exit so scripts never mistake a failed
// start for a healthy daemon.
func (a *App) Run(ctx context.Context, opts ServeOptions) error {
	lock, err := a.acquireLock()
	if err != nil {
		return fmt.Errorf("app: daemon.lock is held — another daemon is already running: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	// the launch snapshot: a silently dying daemon leaves forensics with
	// questions about its environment — the parent, the session, the fd
	// ceiling — answer them up front in daemon-*.log (2026-11-09 hunt).
	var nofile uint64
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err == nil {
		nofile = rl.Cur
	}
	sid, sidErr := syscall.Getsid(os.Getpid())
	if sidErr != nil {
		sid = -1
	}
	a.log.Info("daemon launch",
		"pid", os.Getpid(),
		"ppid", os.Getppid(),
		"sid", sid,
		"rlimit_nofile", nofile,
		"debug", opts.Debug,
	)

	// per-request usage log into <LogDir>/usage-YYYY-MM-DD.jsonl (counts and
	// timings only; Noop outside serve mode, tests swap it)
	_ = os.MkdirAll(a.cfg.LogDir(), 0o750)
	usagelog.Default = usagelog.NewFileRecorder(a.cfg.LogDir())

	// request dumps exist only in debug runs; CCX_DUMP_DIR overrides the
	// directory but can no longer turn dumping on (flag-gated by design:
	// bodies must never land on disk in a normal run). Must precede
	// router.Build — the first served request may already dump.
	reqdump.Configure(reqdump.Options{Enabled: opts.Debug, Dir: a.cfg.LogDir()})

	svc, err := router.Build(ctx, a.cfg, a.log, opts.Provider)
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	port := resolveListenPort(a.cfg, opts.Port)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cl := closer.New()
	cl.Add("router", func() error { return svc.Close(context.Background()) })

	srv, err := httpserver.New(addr, handlers.New(handlers.Deps{
		Router:  svc,
		Version: Version,
		Port:    port,
		Stop:    cancel,
		Log:     a.log,
	}))
	if err != nil {
		_ = cl.Close(context.Background())

		return fmt.Errorf("app: listen %s: %w", addr, err)
	}
	cl.Add("http", func() error {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		return srv.Stop(shutdownCtx)
	})

	a.writePidfile()
	defer func() { _ = os.Remove(a.pidfilePath()) }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Start() }()
	a.log.Info("ccx daemon listening", "addr", addr, "provider", opts.Provider, "version", Version)

	// --debug only: a watchdog dialing the daemon's own /health — a failure
	// while the process is alive separates a broken listener from a dead
	// process (the 2026-11-09 ECONNREFUSED hunt had no way to tell them
	// apart). Stopped BEFORE the closer runs: shutdown closes the listener
	// first and must not fire false alarms during the drain.
	selfDone := make(chan struct{})
	if opts.Debug {
		go selfCheck(a.log, "http://"+addr, selfDone)
	}

	sig := make(chan os.Signal, 1)
	// SIGHUP rides the same ladder: unhandled it kills a Go process with
	// default disposition and zero output — a silent-daemon-death class
	// (verified live in a test: the test binary died with "signal: hangup").
	// Setsid already shields the spawned daemon from terminal HUPs; this
	// catches anything that sends the signal explicitly.
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)

	select {
	case <-ctx.Done():
		// the /admin/stop path: this used to be the daemon's only fully
		// silent exit — the process disappeared without a single log line,
		// leaving ECONNREFUSED forensics with nothing (2026-11-09)
		a.log.Info("daemon stopping", "reason", "context cancelled — /admin/stop or parent")
	case s := <-sig:
		a.log.Info("signal — shutting down", "signal", s.String())
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			close(selfDone)
			_ = cl.Close(context.Background())

			return fmt.Errorf("app: serve: %w", err)
		}
	}
	close(selfDone)

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer closeCancel()

	return cl.Close(closeCtx)
}

// lockRetry is the acquireLock retry window (a var: tests shorten it — the
// default makes a lock-held test wait out the full 5s).
var lockRetry = 5 * time.Second

// resolveListenPort is the daemon-side fallback when --port is absent: env
// CCX_PORT, else the fixed default. providers.json daemon.* is deliberately
// NOT read — that block is switcher-owned launch config, and the switcher
// always passes an explicit --port; daemon.port reaches the daemon through
// the switcher's resolution (config.ResolveDaemonPort), never its own.
func resolveListenPort(c *config.Config, flagPort int) int {
	if flagPort > 0 {
		return flagPort
	}
	if c.DaemonPort > 0 {
		return c.DaemonPort
	}

	return config.DefaultDaemonPort
}

// ResolveListenPortForTest exports resolveListenPort for fallback tests.
var ResolveListenPortForTest = resolveListenPort

// SetLockRetryForTest overrides the lock retry window; ResetLockRetryForTest
// restores it.
func SetLockRetryForTest(d time.Duration) { lockRetry = d }

// ResetLockRetryForTest restores the default lock retry window.
func ResetLockRetryForTest() { lockRetry = 5 * time.Second }

// acquireLock takes daemon.lock with a bounded retry: the previous daemon may
// hold it for a moment while its graceful shutdown drains in-flight requests.
func (a *App) acquireLock() (*flock.Lock, error) {
	// a fresh install may not have the ccx home yet — the lock lives there
	_ = os.MkdirAll(a.cfg.Home, 0o750)

	deadline := time.Now().Add(lockRetry)
	for {
		lock, err := flock.TryLock(a.daemonLockPath())
		if err == nil {
			return lock, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (a *App) daemonLockPath() string {
	return filepath.Join(a.cfg.Home, "daemon.lock")
}

func (a *App) pidfilePath() string {
	return filepath.Join(a.cfg.Home, "daemon.pid")
}

func (a *App) writePidfile() {
	_ = os.MkdirAll(a.cfg.LogDir(), 0o750)
	_ = os.WriteFile(a.pidfilePath(), fmt.Appendf(nil, "%d\n", os.Getpid()), 0o600) //nolint:gosec // fixed permissions
}

// selfCheckInterval — the self-check cadence (a var: tests shorten it).
var selfCheckInterval = 10 * time.Second

// selfCheck dials the daemon's own /health every selfCheckInterval while the
// process is alive: an answering process with a failing self-check means the
// listener is broken, not the process dead — the distinction the 2026-11-09
// silent-death hunt could not make. Every failure is one Warn line, nothing
// else (no restart, no exit): the daemon never self-heals by design — only
// a switch (ccx use) respawns it.
func selfCheck(log *slog.Logger, baseURL string, done <-chan struct{}) {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(selfCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			resp, err := client.Get(baseURL + "/health") //nolint:gosec // the daemon's own address
			if err != nil {
				log.Warn("self-check: listener not answering", "addr", baseURL, "err", err)

				continue
			}
			_ = resp.Body.Close()
		}
	}
}

// SetSelfCheckIntervalForTest overrides the self-check cadence; the returned
// func restores it.
func SetSelfCheckIntervalForTest(d time.Duration) func() {
	selfCheckInterval = d

	return func() { selfCheckInterval = 10 * time.Second }
}

// SelfCheckForTest exports selfCheck for direct tests.
var SelfCheckForTest = selfCheck
