// Package app — daemon wiring for ccx serve: config → logger → closer →
// router (restore from state) → http → signals → graceful shutdown.
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
	"github.com/ivangurin/ccx/internal/services/router"
)

// Version — daemon version (ldflags).
var Version = "dev"

// App — the daemon.
type App struct {
	cfg *config.Config
	log *slog.Logger
}

// New creates an App.
func New(cfg *config.Config, log *slog.Logger) *App {
	return &App{cfg: cfg, log: log}
}

// Run blocks until a signal/ctx cancel (or /admin/stop). A double start is
// prevented: flock daemon.lock, the loser exits quietly (the winning
// process will serve it — a race of two parallel ccx use).
func (a *App) Run(ctx context.Context) error {
	lock, err := flock.TryLock(a.daemonLockPath())
	if err != nil {
		a.log.Info("daemon already running — exiting", "err", err)

		return nil
	}
	defer func() { _ = lock.Unlock() }()

	svc := router.New(a.cfg, a.log)
	svc.Restore(ctx)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cl := closer.New()
	cl.Add("router", func() error { return svc.Close(context.Background()) })

	srv, err := httpserver.New(a.listenAddr(), handlers.New(handlers.Deps{
		Router:  svc,
		Version: Version,
		Stop:    cancel,
	}))
	if err != nil {
		return fmt.Errorf("app: listen %s: %w", a.listenAddr(), err)
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
	a.log.Info("ccx daemon listening", "addr", a.listenAddr(), "version", Version)

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

func (a *App) listenAddr() string {
	port := a.cfg.DaemonPort
	if port == 0 {
		if providers, err := config.LoadProviders(a.cfg.ProvidersPath()); err == nil {
			port = providers.Daemon.Port
		} else {
			port = config.DefaultDaemonPort
		}
	}

	return fmt.Sprintf("127.0.0.1:%d", port)
}

func (a *App) daemonLockPath() string {
	return filepath.Join(a.cfg.CCXDir(), "daemon.lock")
}

func (a *App) pidfilePath() string {
	return filepath.Join(a.cfg.CCXDir(), "daemon.pid")
}

func (a *App) writePidfile() {
	_ = os.MkdirAll(a.cfg.LogDir(), 0o750)
	_ = os.WriteFile(a.pidfilePath(), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600) //nolint:gosec // fixed permissions
}
