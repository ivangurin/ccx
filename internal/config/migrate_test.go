package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// writeV1 writes a bash-ccx-format providers.json file (no version → v1).
func writeV1(t *testing.T, path, raw string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
}

const (
	keyDisableThinking = "CLAUDE_CODE_DISABLE_EXTENDED_THINKING"
	profCopilot        = "copilot"
	profOpenAI         = "openai"
)

func TestMigrateV1_CCRToOpenAI(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := sp.FilePath("providers.json")
	// ccr config unavailable — empty credentials + a warning (no dependence on a live machine)
	config.CCRConfigPath = filepath.Join(t.TempDir(), "absent.json")
	t.Cleanup(func() { config.CCRConfigPath = "" })
	writeV1(t, path, `{"profiles": {"x5": {
		"name": "X5", "proxy": {"start": "ccr start", "health": "http://127.0.0.1:3456", "stop": "ccr stop"},
		"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:3456", "ANTHROPIC_AUTH_TOKEN": "t", "CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true"},
		"models": [{"id": "m1", "label": "M1", "default": true}]
	}}}`)

	res, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)
	require.True(t, res.Migrated)

	p, err := config.LoadProviders(path)
	require.NoError(t, err)
	require.Equal(t, models.ProtocolOpenAI, p.Profiles.Get("x5").Protocol)
	require.NotNil(t, p.Profiles.Get("x5").OpenAI)
	require.Equal(t, []models.Model{{ID: "m1", Label: "M1", Default: true}}, p.Profiles.Get("x5").Models)
	// extras from the v1 env carried over, ANTHROPIC_* did not
	require.Equal(t, map[string]string{"CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true"}, p.Profiles.Get("x5").Env)
	// ccr credentials unavailable (no config.json) — warning
	require.NotEmpty(t, res.Warnings)
	require.Empty(t, p.Profiles.Get("x5").OpenAI.APIKey)
}

func TestMigrateV1_CCRPicksUpCredentials(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := sp.FilePath("providers.json")
	writeV1(t, path, `{"profiles": {"x5": {"name": "X5", "proxy": {"start": "ccr start"},
		"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:3456"}, "models": []}}}`)

	// ccr config in a sandbox (real claude-code-router format)
	ccrPath := filepath.Join(t.TempDir(), "config.json")
	config.CCRConfigPath = ccrPath
	t.Cleanup(func() { config.CCRConfigPath = "" })
	ccrJSON := `{"Providers": [{"name": "x5", "api_base_url": "https://api-copilot.x5.ru/aigw/v1/chat/completions", "api_key": "gw-key", "models": ["m"]}], "Router": {"default": ["x5,m"]}}`
	require.NoError(t, os.WriteFile(ccrPath, []byte(ccrJSON), 0o600))

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	p, err := config.LoadProviders(path)
	require.NoError(t, err)
	require.Equal(t, "https://api-copilot.x5.ru/aigw/v1", p.Profiles.Get("x5").OpenAI.BaseURL)
	require.Equal(t, "gw-key", p.Profiles.Get("x5").OpenAI.APIKey)
}

func TestMigrateV1_CopilotBridgeToCopilot(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	path := sp.FilePath("providers.json")
	writeV1(t, path, `{"profiles": {"copilot": {
		"name": "Copilot",
		"proxy": {"start": "python3 tools/copilot-bridge.py", "health": "http://127.0.0.1:4142", "stop_pattern": "copilot-bridge.py"},
		"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:4142", "ANTHROPIC_AUTH_TOKEN": "ccx-x"},
		"models": [{"id": "claude-sonnet-5.5", "default": true}]
	}}}`)

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	p, err := config.LoadProviders(path)
	require.NoError(t, err)
	prof := p.Profiles.Get(profCopilot)
	require.Equal(t, models.ProtocolCopilot, prof.Protocol)
	require.NotNil(t, prof.Copilot)
	require.NotNil(t, prof.Copilot.PrefillFix)
	require.True(t, *prof.Copilot.PrefillFix)
	require.Equal(t, "claude-sonnet-5.5", prof.Models[0].ID)
}

func TestMigrateV1_ClaudeCodexToCodex(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeV1(t, sp.FilePath("providers.json"), `{"profiles": {"openai": {
		"name": "OpenAI sub", "proxy": {"start": "claude-codex serve --port 18765", "health": "http://127.0.0.1:18765"},
		"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:18765"}, "models": [{"id": "gpt-6.1-codex"}]
	}}}`)

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, models.ProtocolCodex, p.Profiles.Get(profOpenAI).Protocol)
	require.NotNil(t, p.Profiles.Get(profOpenAI).Codex)
}

func TestMigrateV1_DirectToAnthropic(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeV1(t, sp.FilePath("providers.json"), `{"profiles": {"zai": {
		"name": "z.ai", "env": {"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic", "ANTHROPIC_AUTH_TOKEN": "sk-z"},
		"models": [{"id": "glm-5.3[1m]", "default": true}]
	}}}`)

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	prof := p.Profiles.Get("zai")
	require.Equal(t, models.ProtocolAnthropic, prof.Protocol)
	require.Equal(t, "https://api.z.ai/api/anthropic", prof.Anthropic.BaseURL)
	require.Equal(t, "sk-z", prof.Anthropic.AuthToken)
}

func TestMigrateV1_LocalhostWithoutProxy_Warns(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeV1(t, sp.FilePath("providers.json"), `{"profiles": {"p": {
		"name": "local", "env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:3456"}, "models": []
	}}}`)

	res, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)
	require.NotEmpty(t, res.Warnings)
	require.Contains(t, strings.Join(res.Warnings, "\n"), "127.0.0.1:3456")
}

func TestInitProviders_ExistingWithoutForceFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeV1(t, sp.FilePath("providers.json"), `{"profiles": {}}`)

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err) // first generation — v1 migration

	_, err = config.InitProviders(sp.Config, false)
	require.ErrorIs(t, err, config.ErrProvidersExist)

	_, err = config.InitProviders(sp.Config, true) // --force
	require.NoError(t, err)
}

func TestInitProviders_GeneratesDaemon(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeV1(t, sp.FilePath("providers.json"), `{"profiles": {}}`)

	_, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, 2, p.Version)
	require.Equal(t, config.DefaultDaemonPort, p.Daemon.Port)
	require.NotEmpty(t, p.Daemon.AuthToken)
	require.Len(t, p.Daemon.AuthToken, 64) // 32 bytes hex
}

func TestInitProviders_FromEnvironmentTemplates(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	// settings.json with an env block (z.ai) + truncated env.json (x5)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic", "ANTHROPIC_AUTH_TOKEN": "sk-z",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-haiku",
			"API_TIMEOUT_MS": "3000"}
	}`), 0o600))
	require.NoError(t, os.WriteFile(sp.FilePath("env.json"), []byte(
		`"ANTHROPIC_BASE_URL": "https://api-copilot.x5.ru/aigw", "ANTHROPIC_AUTH_TOKEN": "gw", "CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true",`,
	), 0o600))

	res, err := config.InitProviders(sp.Config, false)
	require.NoError(t, err)
	require.False(t, res.Migrated)

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)

	zai := p.Profiles.Get("zai")
	require.NotNil(t, zai)
	require.Equal(t, models.ProtocolAnthropic, zai.Protocol)
	require.Equal(t, "sk-z", zai.Anthropic.AuthToken)
	// primary+haiku models from ANTHROPIC_DEFAULT_*
	require.Equal(t, "glm-5.3[1m]", zai.Models[0].ID)
	require.True(t, zai.Models[0].Default)
	require.Equal(t, "glm-haiku", zai.Models[1].ID)
	// user keys from settings.json are NOT carried into the profile
	require.NotContains(t, zai.Env, "API_TIMEOUT_MS")

	x5 := p.Profiles.Get("x5")
	require.NotNil(t, x5)
	require.Equal(t, "https://api-copilot.x5.ru/aigw", x5.Anthropic.BaseURL)
	// carry-extras: non-ANTHROPIC keys of env.json move over
	require.Equal(t, map[string]string{"CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true"}, x5.Env)

	for _, key := range []string{profOpenAI, profCopilot, "codex"} {
		require.NotNil(t, p.Profiles.Get(key), "template profile %s", key)
	}
	require.Equal(t, models.ProtocolOpenAI, p.Profiles.Get(profOpenAI).Protocol)
	require.Equal(t, models.ProtocolCopilot, p.Profiles.Get(profCopilot).Protocol)
	require.Equal(t, models.ProtocolCodex, p.Profiles.Get("codex").Protocol)
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
