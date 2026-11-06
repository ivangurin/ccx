package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

func sampleProviders() *models.Providers {
	out := &models.Providers{
		Daemon: models.Daemon{Port: 4567},
	}
	out.Profiles.Set("zai", &models.Profile{
		Name:      "z.ai",
		Protocol:  models.ProtocolAnthropic,
		BaseURL:   "https://api.z.ai/api/anthropic",
		AuthToken: "secret",
		Models:    []models.Model{{ID: "glm-5.3[1m]", Label: "GLM 5.3", Default: true}},
	})

	return out
}

func TestSaveLoadProviders_RoundTrip(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := filepath.Join(sp.HomeDir(), "providers.json")

	require.NoError(t, config.SaveProviders(path, sampleProviders()))

	loaded, err := config.LoadProviders(path)
	require.NoError(t, err)
	require.Equal(t, sampleProviders(), loaded)
}

func TestSaveProviders_SetsPermissions600(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := filepath.Join(sp.HomeDir(), "providers.json")

	require.NoError(t, config.SaveProviders(path, sampleProviders()))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadProviders_MissingFile(t *testing.T) {
	_, err := config.LoadProviders(filepath.Join(t.TempDir(), "nope.json"))
	require.Error(t, err)
}

const profOpenAI2 = "openai"

func TestSaveLoadProviders_PreservesProfileOrder(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := filepath.Join(sp.HomeDir(), "providers.json")

	in := &models.Providers{}
	for _, key := range []string{"zai", "x5", profOpenAI2, profCopilot} {
		in.Profiles.Set(key, &models.Profile{Name: key, Protocol: models.ProtocolAnthropic})
	}
	require.NoError(t, config.SaveProviders(path, in))

	loaded, err := config.LoadProviders(path)
	require.NoError(t, err)
	require.Equal(t, []string{"zai", "x5", profOpenAI2, profCopilot}, loaded.Profiles.Keys)

	// order survives a repeated roundtrip too
	require.NoError(t, config.SaveProviders(path, loaded))
	reloaded, err := config.LoadProviders(path)
	require.NoError(t, err)
	require.Equal(t, loaded.Profiles.Keys, reloaded.Profiles.Keys)
}

func TestConfigPaths_DefaultsAndOverrides(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	home := sp.HomeDir()

	require.Equal(t, filepath.Join(home, "providers.json"), sp.Config.ProvidersPath())
	require.Equal(t, filepath.Join(home, "settings.json"), sp.Config.SettingsPath())
	require.Equal(t, filepath.Join(home, "ccms-state.json"), sp.Config.StatePath())
	require.Equal(t, filepath.Join(home, "backups"), sp.Config.BackupDir())
	require.Equal(t, filepath.Join(home, "ccms", "logs"), sp.Config.LogDir())

	t.Setenv("CCMS_PROVIDERS", "/tmp/custom-providers.json")
	t.Setenv("CCMS_SETTINGS", "/tmp/custom-settings.json")
	t.Setenv("CCMS_STATE", "/tmp/custom-state.json")
	t.Setenv("CCMS_BACKUP_DIR", "/tmp/custom-backups")
	t.Setenv("CCMS_LOG_DIR", "/tmp/custom-logs")
	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.Equal(t, "/tmp/custom-providers.json", cfg.ProvidersPath())
	require.Equal(t, "/tmp/custom-settings.json", cfg.SettingsPath())
	require.Equal(t, "/tmp/custom-state.json", cfg.StatePath())
	require.Equal(t, "/tmp/custom-backups", cfg.BackupDir())
	require.Equal(t, "/tmp/custom-logs", cfg.LogDir())
}

// writePortFixture writes providers.json with the given daemon.port
// (nil block = no daemon key at all).
func writePortFixture(t *testing.T, sp *suite_provider.Provider, port *int) {
	t.Helper()
	p := &models.Providers{}
	if port != nil {
		p.Daemon = models.Daemon{Port: *port}
	}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
}

func TestResolveDaemonPort_FlagWins(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writePortFixture(t, sp, newInt(4600))
	sp.Config.DaemonPort = 4699 // CCMS_PORT

	require.Equal(t, 4700, config.ResolveDaemonPort(sp.Config, 4700))
}

func TestResolveDaemonPort_EnvOverProviders(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writePortFixture(t, sp, newInt(4600))
	sp.Config.DaemonPort = 4699 // CCMS_PORT

	require.Equal(t, 4699, config.ResolveDaemonPort(sp.Config, 0))
}

func TestResolveDaemonPort_ProvidersOverDefault(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writePortFixture(t, sp, newInt(4600))

	require.Equal(t, 4600, config.ResolveDaemonPort(sp.Config, 0))
}

func TestResolveDaemonPort_DefaultWhenNoDaemonBlock(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writePortFixture(t, sp, nil) // no daemon key
	sp.Config.DaemonPort = 0

	require.Equal(t, config.DefaultDaemonPort, config.ResolveDaemonPort(sp.Config, 0))
}

// a broken providers.json falls through to the default, not an error.
func TestResolveDaemonPort_BrokenProvidersFallsBack(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{bad`), 0o600)) //nolint:gosec // test sandbox
	sp.Config.DaemonPort = 0

	require.Equal(t, config.DefaultDaemonPort, config.ResolveDaemonPort(sp.Config, 0))
}

func newInt(v int) *int { return &v }

// The schema is unversioned: a file without "version" loads; a legacy file
// with "version": 3 still loads (the unknown field is ignored by the decoder).
func TestLoadProviders_NoVersionRequired(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"profiles":{}}`), 0o600)) //nolint:gosec // test sandbox
	_, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"version":3,"profiles":{}}`), 0o600)) //nolint:gosec // test sandbox
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Empty(t, p.Profiles.Keys)
}

// The daemon block is optional: absent → zero port (the default applies at
// resolution time), profiles intact.
func TestLoadProviders_MissingDaemonBlock(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Profiles.Set("zai", &models.Profile{Name: "z.ai", Protocol: models.ProtocolAnthropic})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	loaded, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, 0, loaded.Daemon.Port)
	require.Equal(t, []string{"zai"}, loaded.Profiles.Keys)
}

func TestSaveLoad_RoundTripKeepsPort(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	in := &models.Providers{Daemon: models.Daemon{Port: 4600}}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), in))

	loaded, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, 4600, loaded.Daemon.Port)
}
