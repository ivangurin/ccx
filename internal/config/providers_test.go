package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

func sampleProviders() *models.Providers {
	out := &models.Providers{
		Daemon: &models.Daemon{Port: 4567},
	}
	out.Providers.Set("zai", &models.Profile{
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
	for _, key := range []string{"zai", "acme", profOpenAI2, profCopilot} {
		in.Providers.Set(key, &models.Profile{Name: key, Protocol: models.ProtocolAnthropic})
	}
	require.NoError(t, config.SaveProviders(path, in))

	loaded, err := config.LoadProviders(path)
	require.NoError(t, err)
	require.Equal(t, []string{"zai", "acme", profOpenAI2, profCopilot}, loaded.Providers.Keys)

	// order survives a repeated roundtrip too
	require.NoError(t, config.SaveProviders(path, loaded))
	reloaded, err := config.LoadProviders(path)
	require.NoError(t, err)
	require.Equal(t, loaded.Providers.Keys, reloaded.Providers.Keys)
}

func TestConfigPaths_DefaultsAndOverrides(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	home := sp.HomeDir()

	require.Equal(t, filepath.Join(home, "providers.json"), sp.Config.ProvidersPath())
	require.Equal(t, filepath.Join(home, "settings.json"), sp.Config.SettingsPath())
	// the one-time backup is a fixed name next to settings.json itself
	require.Equal(t, filepath.Join(home, "settings.json.bak"), sp.Config.SettingsBackupPath())
	require.Equal(t, filepath.Join(home, "state.json"), sp.Config.StatePath())
	require.Equal(t, filepath.Join(home, "logs"), sp.Config.LogDir())

	t.Setenv("CCX_PROVIDERS", "/tmp/custom-providers.json")
	t.Setenv("CCX_SETTINGS", "/tmp/custom-settings.json")
	t.Setenv("CCX_STATE", "/tmp/custom-state.json")
	// CCX_LOG_DIR is gone: logs is pinned to <CCX_HOME>/logs — the env var
	// is ignored
	t.Setenv("CCX_LOG_DIR", "/tmp/custom-logs")
	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.Equal(t, "/tmp/custom-providers.json", cfg.ProvidersPath())
	require.Equal(t, "/tmp/custom-settings.json", cfg.SettingsPath())
	// the backup follows the settings override (always next to the file)
	require.Equal(t, "/tmp/custom-settings.json.bak", cfg.SettingsBackupPath())
	require.Equal(t, "/tmp/custom-state.json", cfg.StatePath())
	require.Equal(t, filepath.Join(cfg.Home, "logs"), cfg.LogDir())
}

func TestSettingsPath_DefaultLivesInClaudeDir(t *testing.T) {
	// settings.json belongs to Claude Code: its default path stays in
	// ~/.claude regardless of CCX_HOME (the sandbox must not capture it,
	// the live settings must not leak into tests) — the escape hatch is
	// CCX_SETTINGS, which the suite provider sets for the other tests.
	t.Setenv("CCX_HOME", "/tmp/some-ccx-home")
	t.Setenv("CCX_SETTINGS", "")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)

	userHome, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(userHome, ".claude", "settings.json"), cfg.SettingsPath())
	require.NotEqual(t, filepath.Join(cfg.Home, "settings.json"), cfg.SettingsPath())
}

// writePortFixture writes providers.json with the given daemon.port
// (nil block = no daemon key at all).
func writePortFixture(t *testing.T, sp *suite_provider.Provider, port *int) {
	t.Helper()
	p := &models.Providers{}
	if port != nil {
		p.Daemon = &models.Daemon{Port: *port}
	}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
}

func TestResolveDaemonPort_FlagWins(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writePortFixture(t, sp, newInt(4600))
	sp.Config.DaemonPort = 4699 // CCX_PORT

	require.Equal(t, 4700, config.ResolveDaemonPort(sp.Config, 4700))
}

func TestResolveDaemonPort_EnvOverProviders(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writePortFixture(t, sp, newInt(4600))
	sp.Config.DaemonPort = 4699 // CCX_PORT

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
	require.Empty(t, p.Providers.Keys)
}

// The daemon block is optional: absent → zero port (the default applies at
// resolution time), profiles intact.
func TestLoadProviders_MissingDaemonBlock(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{Name: "z.ai", Protocol: models.ProtocolAnthropic})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	loaded, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Nil(t, loaded.Daemon)
	require.Equal(t, []string{"zai"}, loaded.Providers.Keys)
}

func TestSaveLoad_RoundTripKeepsPort(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	in := &models.Providers{Daemon: &models.Daemon{Port: 4600}}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), in))

	loaded, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, 4600, loaded.DaemonPort())
}

func TestAuthStorePath_JoinsAuthDir(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	require.Equal(t,
		filepath.Join(sp.HomeDir(), "auth", "gh-work.json"),
		sp.Config.AuthStorePath("gh-work"))
}

func TestLoadProviders_RejectsNonFileNameKeys(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := filepath.Join(sp.HomeDir(), "providers.json")

	// jsonKey: the JSON-encoded form ("\\" is a JSON escape — a raw `\b`
	// would decode to a backspace char, not a backslash).
	for _, tc := range []struct{ key, jsonKey string }{
		{"", ``},
		{"a/b", `a/b`},
		{`a\b`, `a\\b`},
		{"..", `..`},
		{".", `.`},
	} {
		raw := `{"providers": {"` + tc.jsonKey + `": {"name": "x", "protocol": "copilot", "models": []}}}`
		require.NoError(t, os.WriteFile(path, []byte(raw), 0o600)) //nolint:gosec // test sandbox

		_, err := config.LoadProviders(path)
		require.Error(t, err, "key %q must be rejected", tc.key)
		require.Contains(t, err.Error(), "provider key")
	}

	raw := `{"providers": {"gh-work": {"name": "x", "protocol": "copilot", "models": []}}}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600)) //nolint:gosec // test sandbox
	_, err := config.LoadProviders(path)
	require.NoError(t, err)
}
