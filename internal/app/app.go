// Package app — daemon wiring for ccx serve: config → logger → lock →
// route (Ready before listening) → http → signals → graceful shutdown. The
// wiring goes through the service provider (internal/provider): the App
// holds no config or logger of its own — GetConfig/GetLogger/GetRoute come
// from the provider, so the daemon process and the CLI commands compose
// their singletons the same way.
//
// One operation per file: app.go (the App type + Run, the startup
// orchestrator), acquire_lock.go, pidfile.go, self_check.go,
// resolve_listen_port.go.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ivangurin/ccx/internal/api/handlers"
	"github.com/ivangurin/ccx/internal/pkg/closer"
	"github.com/ivangurin/ccx/internal/pkg/httpserver"
	"github.com/ivangurin/ccx/internal/pkg/reqdump"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/provider"
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

// App — the daemon. p is the composition root: config, the diagnostics
// logger and the route all come from it (GetRoute carries the
// Ready-before-listening invariant inside).
type App struct {
	sp       *provider.Provider
	usageLog *slog.Logger
}

// New creates an App over the provider. usageLog is the usage stream over
// the same sink — usage is DATA, not diagnostics: pinned to info+json by
// buildServeLogger so a warn/error CCX_LOG_LEVEL daemon keeps recording (a
// nil usageLog falls back to the provider's logger; tests pass the suite's
// silent logger for both).
func New(sp *provider.Provider, usageLog *slog.Logger) *App {
	if usageLog == nil {
		usageLog = sp.GetLogger()
	}

	return &App{sp: sp, usageLog: usageLog}
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
	log := a.sp.GetLogger()
	cfg := a.sp.GetConfig()

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
	log.Info(
		"daemon launch",
		"pid", os.Getpid(),
		"ppid", os.Getppid(),
		"sid", sid,
		"rlimit_nofile", nofile,
		"debug", opts.Debug,
	)

	// per-request usage rides the unified daemon log (kind=usage JSONL lines;
	// counts and timings only; Noop outside serve mode, tests swap it). The
	// stream is the dedicated usage logger (info+json pinned) — a warn/error
	// env level must never silence usage accounting.
	usagelog.Default = usagelog.NewSlogRecorder(a.usageLog)

	// request dumps exist only in debug runs (flag-gated by design: bodies
	// must never land on disk in a normal run) and ride the unified log as
	// kind=reqdump lines. Must precede the route build — the first served
	// request may already dump.
	reqdump.Configure(reqdump.Options{Enabled: opts.Debug, Logger: log})

	svc, err := a.sp.GetRouteService(ctx, opts.Provider)
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	port := resolveListenPort(cfg, opts.Port)
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
		Log:     log,
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
	log.Info("ccx daemon listening", "addr", addr, "provider", opts.Provider, "version", Version)

	// --debug only: a watchdog dialing the daemon's own /health — a failure
	// while the process is alive separates a broken listener from a dead
	// process (the 2026-11-09 ECONNREFUSED hunt had no way to tell them
	// apart). Stopped BEFORE the closer runs: shutdown closes the listener
	// first and must not fire false alarms during the drain.
	selfDone := make(chan struct{})
	if opts.Debug {
		go selfCheck(log, "http://"+addr, selfDone)
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
		log.Info("daemon stopping", "reason", "context cancelled — /admin/stop or parent")
	case s := <-sig:
		log.Info("signal — shutting down", "signal", s.String())
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
