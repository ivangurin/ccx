package app_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
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

	err = app.New(sp.Config, sp.Logger()).Run(sp.Context(), app.ServeOptions{Provider: "work"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "already running")
}
