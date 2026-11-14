package cli_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/cli"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

func TestExecute_Version(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"version"}, &out)

	require.Equal(t, 0, code)
	require.Equal(t, "ccx dev\n", out.String())
}

func TestExecute_Help(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"help"}, &out)

	require.Equal(t, 0, code)
	for _, want := range []string{"use", "list", "status", "init", "serve", cmdStop, "restore"} {
		require.Contains(t, out.String(), want)
	}
}

func TestExecute_NoArgs_MenuWithoutProviders(t *testing.T) {
	suite_provider.NewProvider(t) // sandbox without providers.json
	var out bytes.Buffer

	code := cli.Execute(nil, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccx init")
}

func TestExecute_UnknownCommand(t *testing.T) {
	// own sandbox: without it the test would read the owner's LIVE ~/.claude
	sp := suite_provider.NewProvider(t)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{}))
	var out bytes.Buffer

	code := cli.Execute([]string{"definitely-not-a-command"}, &out)

	require.NotEqual(t, 0, code)
	// a non-command is treated as a provider (bash parity): provider not found
	require.Contains(t, out.String(), "unknown provider")
}

// restore without a backup (no switch ever froze the original) fails and
// leaves settings.json untouched.
func TestExecute_RestoreNoBackup(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{"env": {}}`), 0o600)) //nolint:gosec // test sandbox
	var out bytes.Buffer

	code := cli.Execute([]string{"restore"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "nothing to restore")
	require.JSONEq(t, `{"env": {}}`, readFile(t, sp.Config.SettingsPath()))
}

func TestExecute_HelpFlag(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"--help"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "use")
}

// `ccx <provider> [model]` is a shortcut for `ccx use <provider> [model]`
// (bash parity): the root command dispatches an unknown first argument to
// use, so a real provider applies just like via the explicit verb.
func TestExecute_ShortcutAppliesUse(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	// an ephemeral port — 4567 belongs to the machine (a live daemon may be
	// serving on it; this test's best-effort Stop must never reach it)
	p := &models.Providers{Daemon: &models.Daemon{Port: freePort(t)}}
	p.Providers.Set(profZai, &models.Profile{
		Name:     profZaiName,
		Protocol: models.ProtocolAnthropic,
		BaseURL:  "https://api.z.ai/api/anthropic",
		Models: []models.Model{
			{ID: "glm-5.3[1m]", Default: true},
			{ID: modelGLMFlash},
		},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{"env": {}}`), 0o600)) //nolint:gosec // test sandbox
	var out bytes.Buffer

	code := cli.Execute([]string{profZai, modelGLMFlash}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), modelGLMFlash)

	st := readStateFile(t, sp.Config.StatePath())
	require.Equal(t, profZai, st.Provider)
	require.Equal(t, modelGLMFlash, st.Model)
}

// The bash-ccx single-dash aliases dispatch to the full commands: each alias
// produces the same output and exit code as its expansion.
func TestExecute_ShortAliases(t *testing.T) {
	for alias, full := range map[string]string{"-l": "list", "-s": "status", "-e": "edit"} { //nolint:paralleltest // table shares nothing
		t.Run(alias, func(t *testing.T) {
			suite_provider.NewProvider(t) // sandbox: commands read the real env
			var aliasOut, fullOut bytes.Buffer

			aliasCode := cli.Execute([]string{alias}, &aliasOut)
			fullCode := cli.Execute([]string{full}, &fullOut)

			require.Equal(t, fullCode, aliasCode)
			require.Equal(t, fullOut.String(), aliasOut.String())
		})
	}
}

func TestExecute_VersionFlag(t *testing.T) {
	for _, flag := range []string{"-v", "--version"} { //nolint:paralleltest // table shares nothing
		t.Run(flag, func(t *testing.T) {
			var out bytes.Buffer

			code := cli.Execute([]string{flag}, &out)

			require.Equal(t, 0, code)
			require.Equal(t, "ccx dev\n", out.String())
		})
	}
}

// `completion` is a real cobra subcommand, not a provider key: the shortcut
// must not swallow it.
func TestExecute_CompletionIsNotShortcut(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"completion", "zsh"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "#compdef ccx")
	require.NotContains(t, out.String(), "unknown provider")
}

// A legacy file with a "version" field loads: the decoder ignores the unknown
// field (the schema is unversioned now).
func TestExecute_LegacyVersionFieldIgnored(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"version": 3, "profiles": {}}`), 0o600)) //nolint:gosec // test sandbox
	var out bytes.Buffer

	code := cli.Execute([]string{"list"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccx init") // no profiles → template hint
}
