// Package app — daemon wiring for ccms serve: config → logger → lock →
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

	"github.com/ivangurin/ccms/internal/api/handlers"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/pkg/closer"
	"github.com/ivangurin/ccms/internal/pkg/flock"
	"github.com/ivangurin/ccms/internal/pkg/httpserver"
	"github.com/ivangurin/ccms/internal/services/router"
)

// Version — daemon version (ldflags).
var Version = "dev"

// ServeOptions — the daemon's launch parameters (ccms serve flags).
type ServeOptions struct {
	// Provider is the providers.json key the route is built from (required).
	Provider string
	// Port is the listen port; 0 = resolve (CCMS_PORT > daemon.port > 4567).
	Port int
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
// Ready(); a startup failure exits non-zero with the reason in daemon.log.
// A double start is prevented by flock daemon.lock; the lock is retried for
// a few seconds first — a restarting daemon may still hold it during its
// graceful shutdown (the spawner waits for the port to close, not the exit).
func (a *App) Run(ctx context.Context, opts ServeOptions) error {
	lock, err := a.acquireLock()
	if err != nil {
		a.log.Info("daemon already running — exiting", "err", err)

		return nil
	}
	defer func() { _ = lock.Unlock() }()

	svc, err := router.Build(ctx, a.cfg, a.log, opts.Provider)
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	port := opts.Port
	if port == 0 {
		port = a.fallbackPort()
	}
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
	a.log.Info("ccms daemon listening", "addr", addr, "provider", opts.Provider, "version", Version)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)

	select {
	case <-ctx.Done():
	case s := <-sig:
		a.log.Info("signal — shutting down", "signal", s.String())
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = cl.Close(context.Background())

			return fmt.Errorf("app: serve: %w", err)
		}
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer closeCancel()

	return cl.Close(closeCtx)
}

// acquireLock takes daemon.lock with a bounded retry: the previous daemon may
// hold it for a moment while its graceful shutdown drains in-flight requests.
func (a *App) acquireLock() (*flock.Lock, error) {
	deadline := time.Now().Add(5 * time.Second)
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

// fallbackPort resolves the listen port when ServeOptions.Port is unset:
// CCMS_PORT > providers.json daemon.port > 4567.
func (a *App) fallbackPort() int {
	if a.cfg.DaemonPort != 0 {
		return a.cfg.DaemonPort
	}
	if providers, err := config.LoadProviders(a.cfg.ProvidersPath()); err == nil && providers.Daemon.Port != 0 {
		return providers.Daemon.Port
	}

	return config.DefaultDaemonPort
}

func (a *App) daemonLockPath() string {
	return filepath.Join(a.cfg.CCMSDir(), "daemon.lock")
}

func (a *App) pidfilePath() string {
	return filepath.Join(a.cfg.CCMSDir(), "daemon.pid")
}

func (a *App) writePidfile() {
	_ = os.MkdirAll(a.cfg.LogDir(), 0o750)
	_ = os.WriteFile(a.pidfilePath(), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600) //nolint:gosec // fixed permissions
}
