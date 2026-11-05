package cli_test

import (
	"bytes"
	"testing"

	"github.com/ivangurin/ccms/internal/cli"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

func TestExecute_Version(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"version"}, &out)

	require.Equal(t, 0, code)
	require.Equal(t, "ccms dev\n", out.String())
}

func TestExecute_Help(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"help"}, &out)

	require.Equal(t, 0, code)
	for _, want := range []string{"use", "list", "status", "init", "serve", "stop"} {
		require.Contains(t, out.String(), want)
	}
}

func TestExecute_NoArgs_MenuWithoutProviders(t *testing.T) {
	suite_provider.NewProvider(t) // sandbox without providers.json
	var out bytes.Buffer

	code := cli.Execute(nil, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccms init")
}

func TestExecute_UnknownCommand(t *testing.T) {
	// own sandbox: without it the test would read the owner's LIVE ~/.claude
	sp := suite_provider.NewProvider(t)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{Version: 2}))
	var out bytes.Buffer

	code := cli.Execute([]string{"definitely-not-a-command"}, &out)

	require.NotEqual(t, 0, code)
	// a non-command is treated as a profile (bash parity): profile not found
	require.Contains(t, out.String(), "unknown profile")
}

func TestExecute_HelpFlag(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"--help"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "use")
}
