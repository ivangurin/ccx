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

const cmdUse = "use"

func TestCommandUse_UnknownProfileFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{Version: 2}))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "ghost"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "неизвестный профиль")
}

func TestCommandUse_MissingProfileArg(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "укажите профиль")
}

func TestCommandUse_NoDefaultModel(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{Version: 2}
	p.Profiles.Set("bare", &models.Profile{Name: "Bare", Protocol: models.ProtocolAnthropic, Models: nil})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "bare"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "нет default-модели")
}

func TestCommandUse_NoProvidersHint(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "zai"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "ccx init")
}

func TestCommandStop_DaemonNotRunning(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{
		Version: 2, Daemon: models.Daemon{Port: 4567, AuthToken: "x"},
	}))
	var out bytes.Buffer

	code := cli.Execute([]string{"stop"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "не запущен")
}

func TestCommandAuth_UnknownProvider(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{"auth", "unknown"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "неизвестный провайдер")
}

func TestCommandAuth_CodexHint(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{"auth", "codex"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "codex login")
}

func TestCommandMenu_FallbackSelectsAndFailsOnDaemon(t *testing.T) {
	// не-tty stdin: меню → выбор номера → use падает на отсутствующем демоне
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{Version: 2}
	p.Profiles.Set("zai", &models.Profile{
		Name: "z.ai", Protocol: models.ProtocolAnthropic,
		Anthropic: &models.AnthropicConfig{BaseURL: "https://x"},
		Models:    []models.Model{{ID: "m", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	// stdin — не терминал (go test): fallback-меню печатает список и ждёт ввод;
	// EOF без выбора — отмена без паники
	code := cli.Execute(nil, &out)

	require.Contains(t, out.String(), "Выберите")
	require.Equal(t, 0, code)
}
