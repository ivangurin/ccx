package cli_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/cli"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
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
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"version": 2, "profiles": {}}`), 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdInit}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "already exists")
	require.Contains(t, out.String(), "--force")
}

func TestCommandInit_ForceRegenerates(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(),
		[]byte(`{"version": 2, "daemon": {"port": 1, "auth_token": "old"}, "profiles": {}}`), 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdInit, "--force"}, &out)

	require.Equal(t, 0, code)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, 4567, p.Daemon.Port)
	require.NotEqual(t, "old", p.Daemon.AuthToken)
}

func writeProviders(t *testing.T, path string, keysInOrder []string) {
	t.Helper()
	p := &models.Providers{Version: 2, Daemon: models.Daemon{Port: 4567, AuthToken: "tok"}}
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
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"version": 2, "profiles": {}}`), 0o600))

	var out bytes.Buffer
	code := cli.Execute([]string{cmdList}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccx init")
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
	require.Contains(t, out.String(), "ccx init")
}
