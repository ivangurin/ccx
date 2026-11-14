package switcher_test

import (
	"context"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/stretchr/testify/require"
)

// no credential in the profile → placeholder token (Claude Code expects one;
// the daemon ignores it for non-pass-through protocols).
func TestApply_NoCredentials_PlaceholderToken(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").AuthToken = ""
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "ccx", env["ANTHROPIC_AUTH_TOKEN"])
}
