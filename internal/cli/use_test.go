package cli_test

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/ivangurin/ccms/internal/cli"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

const cmdUse = "use"

func TestCommandUse_UnknownProfileFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{}))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "ghost"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "unknown profile")
}

func TestCommandUse_MissingProfileArg(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "specify a profile")
}

func TestCommandUse_NoDefaultModel(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("bare", &models.Profile{Name: "Bare", Protocol: models.ProtocolAnthropic, Models: nil})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "bare"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "no default model")
}

// A successful direct switch with an explicit model alias: settings.json and
// state are written inside the sandbox home.
func TestCommandUse_DirectWithExplicitModel(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{Daemon: &models.Daemon{Port: 4567}}
	p.Providers.Set("zai", &models.Profile{
		Name:     "z.ai",
		Protocol: models.ProtocolAnthropic,
		BaseURL:  "https://api.z.ai/api/anthropic",
		Models: []models.Model{
			{ID: "glm-5.3[1m]", Name: "glm-5.3", Default: true},
			{ID: "glm-flash", Name: "flash"},
		},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{"env": {}}`), 0o600)) //nolint:gosec // test sandbox
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "zai", "flash"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "direct")
	require.Contains(t, out.String(), "glm-flash")

	st := readStateFile(t, sp.Config.StatePath())
	require.Equal(t, "zai", st.Profile)
	require.Equal(t, "glm-flash", st.Model)
}

func readStateFile(t *testing.T, path string) models.State {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // test sandbox
	require.NoError(t, err)
	var st models.State
	require.NoError(t, json.Unmarshal(raw, &st))

	return st
}

func TestCommandUse_NoProvidersHint(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "zai"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "ccms init")
}

func TestCommandStop_DaemonNotRunning(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	port := freePort(t)
	t.Setenv("CCMS_PORT", strconv.Itoa(port))
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{
		Daemon: &models.Daemon{Port: port},
	}))
	var out bytes.Buffer

	code := cli.Execute([]string{"stop"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "not running")
}

func TestCommandAuth_UnknownProvider(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{"auth", "unknown"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "unknown provider")
}

func TestCommandAuth_CodexHint(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{"auth", "codex"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "codex login")
}

func TestCommandMenu_FallbackSelectsAndFailsOnDaemon(t *testing.T) {
	// non-tty stdin: menu → number choice → use fails on the missing daemon
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{
		Name: "z.ai", Protocol: models.ProtocolAnthropic,
		BaseURL: "https://x",
		Models:  []models.Model{{ID: "m", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	// stdin is not a terminal (go test): the fallback menu prints the list and waits for input;
	// EOF without a choice — cancel without panic
	code := cli.Execute(nil, &out)

	require.Contains(t, out.String(), "Choose")
	require.Equal(t, 0, code)
}

// freePort — a free TCP port (the test doesn't depend on the default 4567:
// a real daemon may be running on the machine).
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok)
	port := tcpAddr.Port
	require.NoError(t, lis.Close())

	return port
}
