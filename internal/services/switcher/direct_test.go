// Direct-mode tests: protocol=anthropic switches go to the upstream without
// the daemon — the upstream URL and credentials are written into settings.json
// (bash-ccx semantics), the unused daemon is stopped best-effort.
package switcher_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/switcher"
	"github.com/stretchr/testify/require"
)

// newDirectSwitcher — a switcher with a single anthropic profile "p";
// mutate tweaks the flat connection fields (base_url, credentials, headers).
func newDirectSwitcher(t *testing.T, mutate func(p *models.Profile)) (*switcher.Switcher, *fakeDaemon, *suite_provider.Provider) {
	t.Helper()
	sp := suite_provider.NewProvider(t)

	prof := &models.Profile{
		Name:     "P",
		Protocol: models.ProtocolAnthropic,
		Models:   []models.Model{{ID: "m1", Default: true}},
	}
	if mutate != nil {
		mutate(prof)
	}
	p := &models.Providers{Daemon: &models.Daemon{Port: 4567}}
	p.Providers.Set("p", prof)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{"env": {"ANTHROPIC_STALE_KEY": "old"}}`), 0o600))

	d := &fakeDaemon{restartOK: true}
	sw := switcher.New(sp.Config, d)

	return sw, d, sp
}

func TestApply_DirectAnthropic(t *testing.T) {
	sw, d, sp := newTestSwitcher(t) // zai: auth_token based anthropic profile

	out, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.Contains(t, out, "direct")
	require.Contains(t, out, "https://api.z.ai/api/anthropic")

	env := settingsEnv(t, sp.Config.SettingsPath())
	// upstream written directly, daemon token not involved
	require.Equal(t, "https://api.z.ai/api/anthropic", env["ANTHROPIC_BASE_URL"])
	require.Equal(t, "sk-z", env["ANTHROPIC_AUTH_TOKEN"])
	require.NotContains(t, env, "ANTHROPIC_API_KEY")
	// pinning still applies
	for _, key := range pinnedKeysList {
		require.Equal(t, "glm-5.3[1m]", env[key], key)
	}
	// stale keys wiped, user keys kept
	require.NotContains(t, env, "ANTHROPIC_STALE_KEY")
	require.Equal(t, "3000000", env["API_TIMEOUT_MS"])

	// no daemon round-trip; the unused daemon is stopped
	require.Empty(t, d.restarts)
	require.Equal(t, 1, d.stopN)

	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "zai", st.Provider)
	require.Equal(t, "glm-5.3[1m]", st.Model)
}

func TestApply_DirectStopFailureNonFatal(t *testing.T) {
	sw, d, sp := newDirectSwitcher(t, func(p *models.Profile) { p.BaseURL, p.AuthToken = "https://up", "t" })
	d.stopErr = errors.New("daemon did not stop")

	out, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err) // the switch is already committed
	require.Contains(t, out, "still running")
	require.Equal(t, "https://up", settingsEnv(t, sp.Config.SettingsPath())["ANTHROPIC_BASE_URL"])
	require.Equal(t, 1, d.stopN)
}

func TestApply_DirectAPIKeyAndHeaders(t *testing.T) {
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) {
		p.BaseURL = "https://up"
		p.APIKey = "sk-ant"
		p.Headers = map[string]string{"X-Trace": "1", "X-Org": "acme"}
	})

	_, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "sk-ant", env["ANTHROPIC_API_KEY"])
	require.NotContains(t, env, "ANTHROPIC_AUTH_TOKEN") // proxy precedence: one credential wins
	require.Equal(t, "X-Org: acme\nX-Trace: 1", env["ANTHROPIC_CUSTOM_HEADERS"])
}

func TestApply_DirectNoBaseURL(t *testing.T) {
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) { p.AuthToken = "t" })
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "p", "")
	require.ErrorContains(t, err, "base_url")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath())) // untouched
	require.NoFileExists(t, sp.Config.StatePath())
}

// The context-window trio rides the direct path too: a direct anthropic
// profile with a capped model gets the same CLAUDE_CODE_* env.
func TestApply_DirectMaxContextTokens(t *testing.T) {
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) {
		p.BaseURL, p.AuthToken = "https://up", "t"
		p.Models[0].MaxContextTokens = 100000
	})

	_, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "1", env["CLAUDE_CODE_DISABLE_1M_CONTEXT"])
	require.Equal(t, "100000", env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
	require.Equal(t, "90000", env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"])
}

// The effort pin rides the direct path too (pinModel is shared between
// daemonEnv and directEnv): a model with the effort field gets
// CLAUDE_CODE_EFFORT_LEVEL verbatim, a model without it loses the key —
// a stale manual value included.
func TestApply_DirectEffortLevel(t *testing.T) {
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) {
		p.BaseURL, p.AuthToken = "https://up", "t"
	})

	// no effort on m1 → a pre-existing manual value is wiped by the switch
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{"env": {"CLAUDE_CODE_EFFORT_LEVEL": "low"}}`), 0o600)) //nolint:gosec // test sandbox
	_, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)
	require.NotContains(t, settingsEnv(t, sp.Config.SettingsPath()), "CLAUDE_CODE_EFFORT_LEVEL")

	// m1 carries effort → written verbatim
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("p").Models[0].Effort = "xhigh"
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	_, err = sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)
	require.Equal(t, "xhigh", settingsEnv(t, sp.Config.SettingsPath())["CLAUDE_CODE_EFFORT_LEVEL"])
}

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
