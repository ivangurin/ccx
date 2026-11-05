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
		Version: 2,
		Daemon:  models.Daemon{Port: 4567, AuthToken: "tok"},
	}
	out.Profiles.Set("zai", &models.Profile{
		Name:     "z.ai",
		Protocol: models.ProtocolAnthropic,
		Anthropic: &models.AnthropicConfig{
			BaseURL:   "https://api.z.ai/api/anthropic",
			AuthToken: "secret",
		},
		Models: []models.Model{{ID: "glm-5.3[1m]", Label: "GLM 5.3", Default: true}},
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

func TestLoadProviders_RejectsV1(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := filepath.Join(sp.HomeDir(), "providers.json")
	v1 := `{"profiles": {"zai": {"name": "z", "env": {}, "proxy": {"start": "ccr start"}}}}`
	require.NoError(t, os.WriteFile(path, []byte(v1), 0o600))

	_, err := config.LoadProviders(path)
	require.ErrorIs(t, err, config.ErrUnsupportedVersion)
}

func TestLoadProviders_MissingFile(t *testing.T) {
	_, err := config.LoadProviders(filepath.Join(t.TempDir(), "nope.json"))
	require.Error(t, err)
}

const profOpenAI2 = "openai"

func TestSaveLoadProviders_PreservesProfileOrder(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := filepath.Join(sp.HomeDir(), "providers.json")

	in := &models.Providers{Version: 2}
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
	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.Equal(t, "/tmp/custom-providers.json", cfg.ProvidersPath())
	require.Equal(t, "/tmp/custom-settings.json", cfg.SettingsPath())
}
