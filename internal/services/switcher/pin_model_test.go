package switcher_test

import (
	"context"
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/stretchr/testify/require"
)

// legacyPinnedKeys — the old DERIVED multi-mapping pin (computed from the
// selected model, incl. the deprecated SMALL_FAST); a switch must REMOVE
// these from settings.json when they are present. ANTHROPIC_DEFAULT_MODEL
// is not among them anymore: ccx writes it deliberately now.
var legacyPinnedKeys = []string{
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
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

// The per-model effort field lands in settings.json's modelSettings map:
// modelSettings.<bare id>.effortLevel for EVERY provider model carrying
// effort (docs: the map is keyed by the canonical bare id — a [1m]-suffixed
// session model matches the same entry), not only the selected one — so the
// level follows the model through mid-session /model switches. The key is
// ccx-owned WHOLESALE (like availableModels): every switch replaces the map
// with the current provider's entries; a provider without effortful models
// wipes it — manual entries and the legacy env carrier
// (CLAUDE_CODE_EFFORT_LEVEL, the pre-modelSettings channel) included.
func TestApply_EffortLevel(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	// the legacy env leftover dies, the manual foreign entry dies: acme's
	// model has no effort → the whole modelSettings key is wiped
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {"CLAUDE_CODE_EFFORT_LEVEL": "low"},
		"modelSettings": {"old-model": {"effortLevel": "xhigh"}}
	}`), 0o600))
	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.NotContains(t, settingsEnv(t, sp.Config.SettingsPath()), "CLAUDE_CODE_EFFORT_LEVEL")
	require.Empty(t, settingsModelSettings(t, sp.Config.SettingsPath()))

	// effortful models → written verbatim under bare-id keys ([1m] stripped)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	acme := p.Providers.Get("acme")
	acme.Models[0].Effort = "high"
	acme.Models = append(acme.Models, models.Model{ID: "acme-mini[1m]", Label: "Mini", Effort: "xhigh"})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	ms := settingsModelSettings(t, sp.Config.SettingsPath())
	require.Len(t, ms, 2)
	require.Equal(t, "high", ms["copilot-code-large"].EffortLevel)
	require.Equal(t, "xhigh", ms["acme-mini"].EffortLevel, "the [1m] suffix is stripped: keyed by the bare id")

	// switching to a provider without effort → the map dies whole
	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.Empty(t, settingsModelSettings(t, sp.Config.SettingsPath()))
}

// The per-model role flags pin the official ANTHROPIC_DEFAULT_*_MODEL family:
// a flag on a model entry writes THAT model's id into the role's env var —
// explicit config facts, independent of the selected model. The first model
// in list order carrying a role wins it; a role no model carries writes
// nothing.
func TestApply_RoleModelsWritten(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	acme := p.Providers.Get("acme")
	acme.Models[0].Haiku = true
	acme.Models[0].Opus = true
	acme.Models = append(acme.Models, models.Model{ID: "acme-mini", Label: "Mini", Haiku: true, Sonnet: true})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "copilot-code-large", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"], "first model with the flag wins")
	require.Equal(t, "copilot-code-large", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	require.Equal(t, "acme-mini", env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	require.NotContains(t, env, "ANTHROPIC_DEFAULT_FABLE_MODEL", "no model carries the fable flag")
}

// All four role flags on one model point every role var at the same id — and
// the direct anthropic path writes them too, ids verbatim (the [1m] suffix
// stays, same as ANTHROPIC_MODEL).
func TestApply_RoleModelsAllOnOneModel(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	zai := p.Providers.Get("zai")
	zai.Models[0].Haiku = true
	zai.Models[0].Sonnet = true
	zai.Models[0].Opus = true
	zai.Models[0].Fable = true
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	for _, key := range []string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
	} {
		require.Equal(t, "glm-5.3[1m]", env[key], key)
	}
}

// ANTHROPIC_DEFAULT_MODEL — the default of new sessions / the picker's
// Default row: the provider's default-flagged model when it has one, the
// selected model when it does not. ANTHROPIC_MODEL keeps following the
// selection even when the two differ.
func TestApply_DefaultModelPin(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	// zai: glm-5.3[1m] carries default:true; selecting glm-flash → the
	// default pin stays on glm-5.3[1m], the session model is glm-flash
	_, err := sw.Apply(context.Background(), "zai", "glm-flash")
	require.NoError(t, err)
	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "glm-flash", env["ANTHROPIC_MODEL"])
	require.Equal(t, "glm-5.3[1m]", env["ANTHROPIC_DEFAULT_MODEL"], "the default-flagged model is pinned")

	// acme without any default flag → the selected model is pinned (the
	// explicit model arg is required here: no default to resolve empty by)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").Models[0].Default = false
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	_, err = sw.Apply(context.Background(), "acme", "copilot-code-large")
	require.NoError(t, err)
	env = settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "copilot-code-large", env["ANTHROPIC_DEFAULT_MODEL"], "no default flag → the selected model")
	require.Equal(t, "copilot-code-large", env["ANTHROPIC_MODEL"])
}

// No model carries a role flag → the role vars are absent; a manual leftover
// in settings.json dies with the stale sweep (ccx-owned like every
// ANTHROPIC_* key).
func TestApply_RoleModelsAbsent(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "leftover"
		}
	}`), 0o600))

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.NotContains(t, env, "ANTHROPIC_DEFAULT_HAIKU_MODEL")
	require.NotContains(t, env, "ANTHROPIC_DEFAULT_SONNET_MODEL")
}
