package app_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/flock"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// A held daemon.lock means the daemon did not start — Run must return an
// error, not nil: exit 0 with only an Info log made wrapper scripts report a
// healthy daemon that was never started.
func TestRun_LockHeldReturnsError(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	lock, err := flock.LockWait(filepath.Join(sp.HomeDir(), "daemon.lock"), time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Unlock() })
	app.SetLockRetryForTest(200 * time.Millisecond)
	t.Cleanup(app.ResetLockRetryForTest)

	err = app.New(sp.Config, sp.Logger(), nil).Run(sp.Context(), app.ServeOptions{Provider: "work"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "already running")
}

// The daemon never reads providers.json daemon.* — that block is
// switcher-owned launch config (the switcher always passes an explicit
// --port; daemon.debug rides the same spawn). Without --port the daemon
// falls back to CCX_PORT, then the fixed default: daemon.port in the file
// must be invisible here (it applies through the switcher's resolution).
func TestResolveListenPort(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	p := &models.Providers{Daemon: &models.Daemon{Port: 4600}}
	p.Providers.Set("zai", &models.Profile{Name: "z.ai", Protocol: models.ProtocolAnthropic})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	t.Run("flag wins", func(t *testing.T) {
		sp.Config.DaemonPort = 4699 // CCX_PORT — still loses to the flag

		require.Equal(t, 4700, app.ResolveListenPortForTest(sp.Config, 4700))
	})

	t.Run("env CCX_PORT next", func(t *testing.T) {
		sp.Config.DaemonPort = 4699

		require.Equal(t, 4699, app.ResolveListenPortForTest(sp.Config, 0))
	})

	t.Run("default — daemon.port in providers.json is not read", func(t *testing.T) {
		sp.Config.DaemonPort = 0

		require.Equal(t, config.DefaultDaemonPort, app.ResolveListenPortForTest(sp.Config, 0))
	})
}
