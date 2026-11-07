package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// writeLegacyWorld creates the pre-rename on-disk world inside legacyDir:
// providers.json and ccms-state.json directly in ~/.claude, the copilot
// token store in the old ~/.claude/ccms subdir.
func writeLegacyWorld(t *testing.T, legacyDir string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "providers.json"),
		[]byte(`{"providers":{"openai":{"name":"openai"}}}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "ccms-state.json"),
		[]byte(`{"profile":"openai","model":"gpt-5.2"}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "ccms"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "ccms", "copilot-auth.json"),
		[]byte(`{"token":"t"}`), 0o600))
}

func TestMigrateLegacyData_CopiesPreRenameData(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	legacy := t.TempDir()
	writeLegacyWorld(t, legacy)

	require.NoError(t, config.MigrateLegacyData(sp.Config, legacy))

	// providers.json lands verbatim, state is renamed ccms-state → ccx-state;
	// the old flat copilot store is NOT migrated — credentials are
	// per-provider now (<home>/auth/<key>.json), the flat file has no reader
	require.FileExists(t, filepath.Join(sp.HomeDir(), "providers.json"))
	require.FileExists(t, filepath.Join(sp.HomeDir(), "ccx-state.json"))
	require.NoFileExists(t, filepath.Join(sp.HomeDir(), "copilot-auth.json"))

	data, err := os.ReadFile(filepath.Join(sp.HomeDir(), "ccx-state.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"openai"`)

	// providers.json holds tokens: the copy keeps the 0600 permissions
	info, err := os.Stat(filepath.Join(sp.HomeDir(), "providers.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// disposable history is not migrated: backups and logs stay behind
	require.NoDirExists(t, filepath.Join(sp.HomeDir(), "backups"))
	require.NoDirExists(t, filepath.Join(sp.HomeDir(), "ccms"))
}

func TestMigrateLegacyData_NewDataWinsAndOverridesSkip(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	legacy := t.TempDir()
	writeLegacyWorld(t, legacy)

	// the new world already has state — the legacy file must not clobber it
	require.NoError(t, os.WriteFile(sp.Config.StatePath(),
		[]byte(`{"profile":"copilot"}`), 0o600))
	// an explicit CCX_PROVIDERS redirect opts providers.json out of migration
	sp.Config.ProvidersFile = "/tmp/custom-providers.json"

	require.NoError(t, config.MigrateLegacyData(sp.Config, legacy))

	data, err := os.ReadFile(sp.Config.StatePath())
	require.NoError(t, err)
	require.Contains(t, string(data), `"copilot"`)
	require.NoFileExists(t, filepath.Join(sp.HomeDir(), "providers.json"))
}

func TestMigrateLegacyData_EmptyLegacyWorldIsNoop(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	require.NoError(t, config.MigrateLegacyData(sp.Config, t.TempDir()))

	require.NoFileExists(t, sp.Config.ProvidersPath())
	require.NoFileExists(t, sp.Config.StatePath())
	require.NoFileExists(t, filepath.Join(sp.HomeDir(), "copilot-auth.json"))
}
