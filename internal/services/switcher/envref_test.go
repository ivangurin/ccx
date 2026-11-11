// envref tests: auth_token/api_key may be a whole-value env reference
// ($NAME / ${NAME}). The switcher expands it at the point of consumption —
// settings.json receives the literal secret, providers.json keeps the
// reference; an unset/empty variable fails the switch BEFORE anything is
// touched (no daemon restart, no settings edit, no state).
package switcher_test

import (
	"context"
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/stretchr/testify/require"
)

// envRefUpstream — dummy upstream URL for the direct-profile fixtures.
const envRefUpstream = "https://up"

// A direct anthropic profile whose auth_token is an env reference: the
// expanded value lands in settings.json (the reference never does).
func TestApply_EnvRef_DirectExpanded(t *testing.T) {
	t.Setenv("CCX_SWITCH_TOKEN", "sk-from-env")
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) {
		p.BaseURL, p.AuthToken = envRefUpstream, "$CCX_SWITCH_TOKEN"
	})

	_, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "sk-from-env", env["ANTHROPIC_AUTH_TOKEN"])
}

// The api_key field gets the same treatment (openai Bearer / anthropic
// x-api-key are both secrets).
func TestApply_EnvRef_APIKeyExpanded(t *testing.T) {
	t.Setenv("CCX_SWITCH_KEY", "sk-key-env")
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) {
		p.BaseURL, p.APIKey = envRefUpstream, "${CCX_SWITCH_KEY}"
	})

	_, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)

	require.Equal(t, "sk-key-env", settingsEnv(t, sp.Config.SettingsPath())["ANTHROPIC_API_KEY"])
}

// An unset variable fails the direct switch before any write: settings.json
// untouched, no state, the daemon was never stopped (it never got that far).
func TestApply_EnvRefMissing_DirectFailsUntouched(t *testing.T) {
	sw, d, sp := newDirectSwitcher(t, func(p *models.Profile) {
		p.BaseURL, p.AuthToken = envRefUpstream, "$CCX_SWITCH_MISSING"
	})
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "p", "")
	require.ErrorContains(t, err, "CCX_SWITCH_MISSING")
	require.ErrorContains(t, err, "untouched")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
	require.NoFileExists(t, sp.Config.StatePath())
	require.Equal(t, 0, d.stopN, "nothing was stopped — the switch died before any side effect")
}

// A translated profile must fail BEFORE the daemon restart: an unset
// variable discovered after Restart would have killed the live daemon for
// nothing and forced a rollback. The expansion runs first; Restart is never
// called.
func TestApply_EnvRefMissing_TranslatedNoRestart(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	acme := p.Providers.Get("acme")
	acme.AuthToken, acme.APIKey = "$CCX_SWITCH_MISSING", ""
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	before := readSettings(t, sp.Config.SettingsPath())

	_, err = sw.Apply(context.Background(), "acme", "")
	require.ErrorContains(t, err, "CCX_SWITCH_MISSING")
	require.Empty(t, d.snapshotRestarts(), "the daemon was not restarted")
	require.Equal(t, 0, d.stopN)
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
	require.NoFileExists(t, sp.Config.StatePath())
}

// The reference stays in providers.json after a switch — only settings.json
// ever sees the secret.
func TestApply_EnvRef_ProvidersKeepReference(t *testing.T) {
	t.Setenv("CCX_SWITCH_TOKEN", "sk-from-env")
	sw, _, sp := newDirectSwitcher(t, func(p *models.Profile) {
		p.BaseURL, p.AuthToken = envRefUpstream, "$CCX_SWITCH_TOKEN"
	})

	_, err := sw.Apply(context.Background(), "p", "")
	require.NoError(t, err)

	raw, err := os.ReadFile(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Contains(t, string(raw), "$CCX_SWITCH_TOKEN")
	require.NotContains(t, string(raw), "sk-from-env")
}
