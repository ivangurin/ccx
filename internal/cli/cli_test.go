package cli_test

import (
	"bytes"
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
	for _, want := range []string{"use", "list", "status", "init", "serve", "stop"} {
		require.Contains(t, out.String(), want)
	}
}

func TestExecute_NoArgs_MenuWithoutProviders(t *testing.T) {
	suite_provider.NewProvider(t) // песочница без providers.json
	var out bytes.Buffer

	code := cli.Execute(nil, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccx init")
}

func TestExecute_UnknownCommand(t *testing.T) {
	// своя песочница: без неё тест читал бы ЖИВОЙ ~/.claude владельца
	sp := suite_provider.NewProvider(t)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{Version: 2}))
	var out bytes.Buffer

	code := cli.Execute([]string{"definitely-not-a-command"}, &out)

	require.NotEqual(t, 0, code)
	// не-команда трактуется как профиль (паритет bash): профиль не найден
	require.Contains(t, out.String(), "неизвестный профиль")
}

func TestExecute_HelpFlag(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"--help"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "use")
}
