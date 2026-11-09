package app_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// recordingLogger captures everything emitted through slog at Info+ into a
// buffer the test greps (the suite's own logger is silent by design).
func recordingLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// freePort reserves an ephemeral port and releases it for the daemon to bind.
func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcp, ok := l.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener address is not TCP: %v", l.Addr())
	require.NoError(t, l.Close())

	return tcp.Port
}

// prepareRoute writes a minimal anthropic provider (Ready is nil — no
// network at startup) so Run can reach the listening stage.
func prepareRoute(t *testing.T, sp *suite_provider.Provider) {
	t.Helper()

	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{
		Name:     "z.ai",
		Protocol: models.ProtocolAnthropic,
		BaseURL:  "https://example.invalid",
		Models:   []models.Model{{ID: "glm", Label: "GLM", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
}

// The ctx-cancel exit (the /admin/stop path) used to be the only fully
// silent daemon exit: the process disappeared without a single log line,
// which made live ECONNREFUSED forensics impossible (2026-11-09: the daemon
// died silently every 1-24 min and nothing said why). Run must log WHY it
// is stopping on this path too.
func TestRun_ContextCancelLogsStopping(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	prepareRoute(t, sp)

	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(sp.Context())
	cancel() // pre-cancelled: the select must take ctx.Done immediately

	err := app.New(sp.Config, recordingLogger(&buf)).Run(ctx, app.ServeOptions{
		Provider: "zai",
		Port:     freePort(t),
	})

	require.NoError(t, err)
	require.Contains(t, buf.String(), "daemon stopping")
	require.Contains(t, buf.String(), "context cancelled")

	// the launch snapshot: when a daemon dies silently, the first question
	// is what its environment looked like (parent, session, fd ceiling)
	require.Contains(t, buf.String(), "daemon launch")
	require.Contains(t, buf.String(), "ppid")
}

// SIGHUP is not a terminal closing (Setsid protects the daemon from that) —
// but whatever DOES send it must not kill the process silently: unhandled
// SIGHUP terminates a Go process with default disposition and zero output,
// which matched the observed silent-death signature exactly. Handling it
// like SIGINT/SIGTERM turns that class into a logged graceful shutdown.
func TestRun_SighupGracefulShutdown(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	prepareRoute(t, sp)

	var buf bytes.Buffer
	port := freePort(t)

	var wg sync.WaitGroup
	wg.Go(func() {
		err := app.New(sp.Config, recordingLogger(&buf)).Run(sp.Context(), app.ServeOptions{
			Provider: "zai",
			Port:     port,
		})
		require.NoError(t, err)
	})

	// the pidfile appears after net.Listen — the daemon is serving by then
	pidPath := filepath.Join(sp.HomeDir(), "daemon.pid")
	require.Eventually(t, func() bool {
		_, err := os.Stat(pidPath)

		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "daemon never wrote its pidfile")

	raw, err := os.ReadFile(pidPath) //nolint:gosec // the daemon's own pidfile in the sandbox home
	require.NoError(t, err)
	pid, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	require.NoError(t, err)

	require.NoError(t, syscall.Kill(int(pid), syscall.SIGHUP))
	wg.Wait()

	require.Contains(t, buf.String(), "signal — shutting down")
	require.Contains(t, buf.String(), "hangup") // Go's Signal.String() for SIGHUP
}
