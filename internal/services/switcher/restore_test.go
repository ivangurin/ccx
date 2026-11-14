package switcher_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

func TestRestoreOriginal_NoBackup(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.RestoreOriginal(context.Background())

	require.ErrorContains(t, err, "nothing to restore")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
}

func TestRestoreOriginal_RestoresAndStopsDaemon(t *testing.T) {
	sw, rec, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.NotEqual(t, readSettings(t, sp.Config.SettingsBackupPath()), readSettings(t, sp.Config.SettingsPath()))
	stopsBefore := rec.stops // applyDirect already stopped the idle daemon once

	out, err := sw.RestoreOriginal(context.Background())

	require.NoError(t, err)
	require.Contains(t, out, sp.Config.SettingsPath())
	require.Equal(t, readSettings(t, sp.Config.SettingsBackupPath()), readSettings(t, sp.Config.SettingsPath()))
	require.Equal(t, stopsBefore+1, rec.stops)
}

func TestRestoreOriginal_DaemonStopFailureIsNote(t *testing.T) {
	sw, rec, sp := newTestSwitcher(t)
	require.NoError(t, os.WriteFile(sp.Config.SettingsBackupPath(), []byte(`{"env": {}}`), 0o600)) //nolint:gosec // test sandbox
	rec.stopErr = errors.New("refused")

	out, err := sw.RestoreOriginal(context.Background())

	require.NoError(t, err)
	require.Contains(t, out, "note:")
	require.JSONEq(t, `{"env": {}}`, readSettings(t, sp.Config.SettingsPath()))
}

func TestRestoreOriginal_KeepsTargetPerms(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	// the fixture created settings.json at 0600; os.WriteFile ignores perm on
	// an existing file — retighten via Chmod (umask-independent)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{"env": {}}`), 0o600))               //nolint:gosec // test sandbox
	require.NoError(t, os.Chmod(sp.Config.SettingsPath(), 0o644))                                          //nolint:gosec // a looser target is the point: restore must keep it
	require.NoError(t, os.WriteFile(sp.Config.SettingsBackupPath(), []byte(`{"env": {"X": "1"}}`), 0o600)) //nolint:gosec // test sandbox

	_, err := sw.RestoreOriginal(context.Background())

	require.NoError(t, err)
	info, err := os.Stat(sp.Config.SettingsPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestRestoreOriginal_CorruptBackupRefused(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	before := readSettings(t, sp.Config.SettingsPath())
	require.NoError(t, os.WriteFile(sp.Config.SettingsBackupPath(), []byte(`{not json`), 0o600)) //nolint:gosec // test sandbox

	_, err := sw.RestoreOriginal(context.Background())

	require.ErrorContains(t, err, "not valid JSON")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
}

func TestRestoreOriginal_ResetsState(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.FileExists(t, sp.Config.StatePath())

	_, err = sw.RestoreOriginal(context.Background())
	require.NoError(t, err)

	// no provider is on record after a restore: rollbackDaemon must have
	// nothing to resurrect on a later failed switch, ccx status nothing to
	// report as active
	var state models.State
	require.NoError(t, jsonfile.ReadJSON(sp.Config.StatePath(), &state))
	require.Empty(t, state.Provider)
	require.Empty(t, state.Model)
}
