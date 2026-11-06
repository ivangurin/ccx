package config_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

const (
	keyDisableThinking = "CLAUDE_CODE_DISABLE_EXTENDED_THINKING"
	profCopilot        = "copilot"
	profOpenAI         = "openai"
)

func TestInitProviders_ExistingWithoutForceFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err) // first generation

	_, err = config.InitProviders(sp.Config, false)
	require.ErrorIs(t, err, config.ErrProvidersExist)

	_, err = config.InitProviders(sp.Config, true) // --force
	require.NoError(t, err)
}

// Init writes neither a version nor a daemon block: no daemon token exists
// (pass-through auth), and the port resolves via ResolveDaemonPort.
func TestInit_NoVersionNoTokenWritten(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	raw, err := os.ReadFile(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"version":`)
	require.NotContains(t, string(raw), "auth_token")
	require.NotContains(t, string(raw), `"profiles"`)
	require.Contains(t, string(raw), `"providers"`)
	// the masquerade is code constants now — init never writes it
	require.NotContains(t, string(raw), "user_agent")
	require.NotContains(t, string(raw), "integration_id")
	require.NotContains(t, string(raw), "editor_version")
	require.NotContains(t, string(raw), "originator")
	// no port baked in: resolution owns the default
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Nil(t, p.Daemon)
}

func TestInitProviders_FromEnvironmentTemplates(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	// settings.json with an env block (z.ai) + truncated env.json (work)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic", "ANTHROPIC_AUTH_TOKEN": "sk-z",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-haiku",
			"API_TIMEOUT_MS": "3000"}
	}`), 0o600))
	require.NoError(t, os.WriteFile(sp.FilePath("env.json"), []byte(
		`"ANTHROPIC_BASE_URL": "https://api-copilot.work.example/aigw", "ANTHROPIC_AUTH_TOKEN": "gw", "CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true",`,
	), 0o600))

	res, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)

	zai := p.Providers.Get("zai")
	require.NotNil(t, zai)
	require.Equal(t, models.ProtocolAnthropic, zai.Protocol)
	require.Equal(t, "sk-z", zai.AuthToken)
	// primary+haiku models from ANTHROPIC_DEFAULT_*
	require.Equal(t, "glm-5.3[1m]", zai.Models[0].ID)
	require.True(t, zai.Models[0].Default)
	require.Equal(t, "glm-haiku", zai.Models[1].ID)
	// user keys from settings.json are NOT carried into the profile
	require.NotContains(t, zai.Env, "API_TIMEOUT_MS")

	work := p.Providers.Get("work")
	require.NotNil(t, work)
	require.Equal(t, "https://api-copilot.work.example/aigw", work.BaseURL)
	// carry-extras: non-ANTHROPIC keys of env.json move over
	require.Equal(t, map[string]string{keyDisableThinking: "true"}, work.Env)

	for _, key := range []string{profOpenAI, profCopilot, "codex"} {
		require.NotNil(t, p.Providers.Get(key), "template profile %s", key)
	}
	require.Equal(t, models.ProtocolOpenAI, p.Providers.Get(profOpenAI).Protocol)
	require.Equal(t, models.ProtocolCopilot, p.Providers.Get(profCopilot).Protocol)
	require.Equal(t, models.ProtocolCodex, p.Providers.Get("codex").Protocol)

	// the flat layout: connection fields sit directly in the profile
	require.Equal(t, "https://api.openai.com/v1", p.Providers.Get(profOpenAI).BaseURL)
	require.Equal(t, "~/.codex/auth.json", p.Providers.Get("codex").AuthFile)
	require.Empty(t, res.Warnings)
}

func TestInitProviders_SettingsUntouched(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	settings := sp.Config.SettingsPath()
	require.NoError(t, os.WriteFile(settings, []byte(`{"env": {"ANTHROPIC_BASE_URL": "https://x"}}`), 0o600)) //nolint:gosec // test sandbox

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	data, err := os.ReadFile(settings) //nolint:gosec // test sandbox
	require.NoError(t, err)
	var raw struct {
		Env map[string]string `json:"env"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Equal(t, "https://x", raw.Env["ANTHROPIC_BASE_URL"])
}
