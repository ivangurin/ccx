// Direct-mode tests: protocol=anthropic switches go to the upstream without
// the daemon — the upstream URL and credentials are written into settings.json
// (bash-ccx semantics), the unused daemon is stopped best-effort.
package switcher_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/ivangurin/ccms/internal/services/switcher"
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
	require.Equal(t, "zai", st.Profile)
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
		p.Headers = map[string]string{"X-Trace": "1", "X-Org": "x5"}
	})

	_, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "sk-ant", env["ANTHROPIC_API_KEY"])
	require.NotContains(t, env, "ANTHROPIC_AUTH_TOKEN") // proxy precedence: one credential wins
	require.Equal(t, "X-Org: x5\nX-Trace: 1", env["ANTHROPIC_CUSTOM_HEADERS"])
}

func TestApply_DirectNoBaseURL(t *testing.T) {
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) { p.AuthToken = "t" })
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "p", "")
	require.ErrorContains(t, err, "base_url")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath())) // untouched
	require.NoFileExists(t, sp.Config.StatePath())
}

// pinnedKeysList — copy of switcher's pinned keys for assertions.
var pinnedKeysList = []string{
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
}
