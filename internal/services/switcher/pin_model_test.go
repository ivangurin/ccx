package switcher_test

import (
	"context"
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/stretchr/testify/require"
)

// pinnedKeysList — the model pin env vars ccx writes (just one: the legacy
// ANTHROPIC_DEFAULT_*_MODEL / SMALL_FAST mappings are deliberately not
// written anymore — a leftover in settings.json is wiped by the stale sweep).
var pinnedKeysList = []string{
	"ANTHROPIC_MODEL",
}

// legacyPinnedKeys — the old multi-mapping pin; a switch must REMOVE these
// from settings.json when they are present.
var legacyPinnedKeys = []string{
	"ANTHROPIC_DEFAULT_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_SONET_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
}

// max_context_tokens on the selected model caps the window Claude Code
// believes it has: the 1M context is disabled, the window pinned, and the
// auto-compact threshold set to −10%; a model WITHOUT the parameter wipes all
// three on the next switch (they are ccx-owned, not user keys).
func TestApply_MaxContextTokens(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").Models[0].MaxContextTokens = 200000
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "1", env["CLAUDE_CODE_DISABLE_1M_CONTEXT"])
	require.Equal(t, "200000", env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
	require.Equal(t, "180000", env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"], "auto-compact window = max − 10%%")

	// zai's model has no max_context_tokens → the trio is wiped
	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	env = settingsEnv(t, sp.Config.SettingsPath())
	require.NotContains(t, env, "CLAUDE_CODE_DISABLE_1M_CONTEXT")
	require.NotContains(t, env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS")
	require.NotContains(t, env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
}

// The per-model effort field pins CLAUDE_CODE_EFFORT_LEVEL verbatim (no
// validation — the value passes through as written): written when the model
// carries it, wiped on the next switch when it does not. The key is
// ccx-owned: a manual value in settings.json dies too (stale-managed
// semantics, same as the context trio).
func TestApply_EffortLevel(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	// acme's model has no effort → a pre-existing manual value is wiped
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {
			"CLAUDE_CODE_EFFORT_LEVEL": "low"
		}
	}`), 0o600))
	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.NotContains(t, settingsEnv(t, sp.Config.SettingsPath()), "CLAUDE_CODE_EFFORT_LEVEL")

	// a model with the effort field → written verbatim
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").Models[0].Effort = "high"
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.Equal(t, "high", settingsEnv(t, sp.Config.SettingsPath())["CLAUDE_CODE_EFFORT_LEVEL"])

	// back to a model without effort → wiped again
	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.NotContains(t, settingsEnv(t, sp.Config.SettingsPath()), "CLAUDE_CODE_EFFORT_LEVEL")
}
