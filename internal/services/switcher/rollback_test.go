package switcher_test

import (
	"context"
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/stretchr/testify/require"
)

// pinned Restart call sequences ("provider:port" per call)
const (
	restartAcme = "acme:4567"
	restartBeta = "beta:4567"
)

// addBeta registers a second translated provider (the rollback target's
// twin: switching between two daemon-routed providers is the case where a
// dead daemon breaks the running session).
func addBeta(t *testing.T, sp interface {
	ProvidersPath() string
},
) {
	t.Helper()
	p, err := config.LoadProviders(sp.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Set("beta", &models.Profile{
		Name:     "Beta",
		Protocol: models.ProtocolOpenAI,
		BaseURL:  "https://gw2",
		APIKey:   "k2",
		Env:      map[string]string{},
		Models:   []models.Model{{ID: "beta-model", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.ProvidersPath(), p))
}

// A failed restart must RESPAWN the previous provider's daemon: the
// invariant promises "you stay on the previous provider", and for a
// translated previous provider that promise is the daemon itself — Stop
// already killed it during the failed Restart.
func TestApply_RestartFailureRestoresPreviousDaemon(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	addBeta(t, sp.Config)

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	d.restartOK = false
	_, err = sw.Apply(context.Background(), "beta", "")
	require.ErrorContains(t, err, "untouched")
	require.Equal(t, []string{restartAcme, restartBeta, restartAcme}, d.snapshotRestarts(),
		"the failed switch must be followed by a rollback restart of the previous provider")
}

// When the rollback restart fails too, the error must say so and carry the
// recovery command — a silent "you stay on the previous provider" would be
// a lie with no daemon running.
func TestApply_RestartFailureRollbackAlsoFails(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	addBeta(t, sp.Config)

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	d.restartOK = false
	_, err = sw.Apply(context.Background(), "beta", "")
	require.ErrorContains(t, err, "did not come back up")
	require.ErrorContains(t, err, "run: ccx acme")
	require.Equal(t, []string{restartAcme, restartBeta, restartAcme}, d.snapshotRestarts())
}

// A settings.json failure AFTER a successful restart: the new daemon is
// already serving while the untouched settings.json still names the old
// provider (with its credential) — the daemon must be rolled back for the
// old env block to work again.
func TestApply_SettingsWriteFailureRollsDaemonBack(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	addBeta(t, sp.Config)

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	require.NoError(t, os.Remove(sp.Config.SettingsPath()))
	_, err = sw.Apply(context.Background(), "beta", "")
	require.ErrorContains(t, err, "created by the first run of Claude Code")
	require.ErrorContains(t, err, "stay on the previous provider")
	require.Equal(t, []string{restartAcme, restartBeta, restartAcme}, d.snapshotRestarts())
}

// A DIRECT previous provider (anthropic passthrough) needs no rollback:
// settings.json still points straight at its upstream, no daemon was
// serving it.
func TestApply_RestartFailure_DirectPreviousNeedsNoRestore(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	addBeta(t, sp.Config)

	_, err := sw.Apply(context.Background(), "zai", "") // direct: no daemon
	require.NoError(t, err)

	d.restartOK = false
	_, err = sw.Apply(context.Background(), "beta", "")
	require.ErrorContains(t, err, "untouched")
	require.Equal(t, []string{restartBeta}, d.snapshotRestarts(),
		"no rollback restart: the previous provider is a direct anthropic profile")
}
