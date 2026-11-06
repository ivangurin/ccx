package cli_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ivangurin/ccms/internal/cli"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

const (
	cmdInit   = "init"
	cmdList   = "list"
	cmdStatus = "status"
	cmdEdit   = "edit"
	profZai   = "zai"
)

func TestCommandInit_CreatesProviders(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdInit}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Created")
	require.FileExists(t, sp.Config.ProvidersPath())
}

func TestCommandInit_SecondRunSuggestsForce(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"version": 3, "profiles": {}}`), 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdInit}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "already exists")
	require.Contains(t, out.String(), "--force")
}

func TestCommandInit_ForceRegenerates(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(),
		[]byte(`{"version": 3, "daemon": {"port": 1, "auth_token": "old"}, "profiles": {}}`), 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdInit, "--force"}, &out)

	require.Equal(t, 0, code)
	raw, err := os.ReadFile(sp.Config.ProvidersPath())
	require.NoError(t, err)
	// the regenerated file is the current schema: no version, no daemon
	// token, the map under "providers" (never the legacy "profiles" key)
	require.NotContains(t, string(raw), `"version":`)
	require.NotContains(t, string(raw), "auth_token")
	require.NotContains(t, string(raw), `"profiles"`)
	require.Contains(t, string(raw), `"providers"`)
}

func writeProviders(t *testing.T, path string, keysInOrder []string) {
	t.Helper()
	p := &models.Providers{Daemon: &models.Daemon{Port: 4567}}
	for _, k := range keysInOrder {
		p.Profiles.Set(k, &models.Profile{
			Name:     "name-" + k,
			Protocol: models.ProtocolAnthropic,
			Models: []models.Model{
				{ID: k + "-model", Label: "Model " + k, Default: true},
				{ID: k + "-alt"},
			},
		})
	}
	require.NoError(t, config.SaveProviders(path, p))
}

func TestCommandList_OrdersByFile(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp.Config.ProvidersPath(), []string{profZai, "x5", "copilot"})

	var out bytes.Buffer
	code := cli.Execute([]string{cmdList}, &out)

	require.Equal(t, 0, code)
	lines := out.String()
	zaiPos := strings.Index(lines, profZai)
	x5Pos := strings.Index(lines, "x5")
	copilotPos := strings.Index(lines, "copilot")
	require.Less(t, zaiPos, x5Pos)
	require.Less(t, x5Pos, copilotPos)
	require.Contains(t, lines, "zai-model")
	require.Contains(t, lines, "default")
}

func TestCommandList_NoProvidersHint(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"version": 3, "profiles": {}}`), 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdList}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccms init")
}

func TestCommandStatus_WithState(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp.Config.ProvidersPath(), []string{profZai})
	require.NoError(t, jsonfile.WriteAtomicJSON(sp.Config.StatePath(),
		models.State{Profile: profZai, Model: "zai-model", Updated: "2026-11-05T10:00:00Z"}, 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdStatus}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), profZai)
	require.Contains(t, out.String(), "zai-model")
	require.Contains(t, out.String(), "name-zai")
}

func TestCommandStatus_NoState(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp.Config.ProvidersPath(), []string{profZai})

	var out bytes.Buffer
	code := cli.Execute([]string{cmdStatus}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "No active profile")
}

func TestCommandStatus_UnknownProfileInState(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp.Config.ProvidersPath(), []string{profZai})
	require.NoError(t, jsonfile.WriteAtomicJSON(sp.Config.StatePath(), models.State{Profile: "gone", Model: "m"}, 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdStatus}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "gone")
}

func TestCommandEdit_OpensEditor(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp.Config.ProvidersPath(), []string{profZai})
	t.Setenv("EDITOR", "/usr/bin/true")

	var out bytes.Buffer
	code := cli.Execute([]string{cmdEdit}, &out)

	require.Equal(t, 0, code)
}

func TestCommandEdit_MissingFileFails(t *testing.T) {
	suite_provider.NewProvider(t) // sandbox without providers.json
	t.Setenv("EDITOR", "/usr/bin/true")

	var out bytes.Buffer
	code := cli.Execute([]string{cmdEdit}, &out)

	require.NotEqual(t, 0, code)
}

func TestCommand_MissingProvidersInitHint(t *testing.T) {
	suite_provider.NewProvider(t) // sandbox without providers.json

	var out bytes.Buffer
	code := cli.Execute([]string{cmdList}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccms init")
}

func TestServe_RequiresProvider(t *testing.T) {
	suite_provider.NewProvider(t) // sandbox: a broken config must not matter

	var out bytes.Buffer
	code := cli.Execute([]string{"serve"}, &out)

	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "--provider")
}

func TestServe_FlagParsing(t *testing.T) {
	opts, err := cli.ParseServeForTest([]string{"--provider", "work", "--port", "4600", "--daemon"})
	require.NoError(t, err)
	require.Equal(t, "work", opts.Provider)
	require.Equal(t, 4600, opts.Port)
}

func TestServe_FlagParsing_UnknownFlagFails(t *testing.T) {
	_, err := cli.ParseServeForTest([]string{"--provider", "work", "--nonsense"})
	require.Error(t, err)
}
