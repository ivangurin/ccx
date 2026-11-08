package cli_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/cli"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/switcher"
	"github.com/stretchr/testify/require"
)

const cmdUse = "use"

func TestCommandUse_UnknownProfileFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{}))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "ghost"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "unknown provider")
}

func TestCommandUse_MissingProfileArg(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "specify a provider")
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
	require.Equal(t, "zai", st.Provider)
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
	require.Contains(t, out.String(), "ccx init")
}

// A corrupt providers.json must not advertise ccx init (see
// TestCommandList_CorruptProvidersFails) — the real error instead.
func TestCommandUse_CorruptProvidersFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(`{"providers":`), 0o600))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdUse, "zai"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "providers.json")
	require.NotContains(t, out.String(), "ccx init")
}

func TestCommandStop_DaemonNotRunning(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	port := freePort(t)
	t.Setenv("CCX_PORT", strconv.Itoa(port))
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), &models.Providers{
		Daemon: &models.Daemon{Port: port},
	}))
	var out bytes.Buffer

	code := cli.Execute([]string{"stop"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "not running")
}

// A daemon that ignores /admin/stop (a hung SSE stream can hold the graceful
// drain forever) must fail the command: exit 0 "still responding" told
// scripts the port was free while the next spawn then lost the lock race.
func TestCommandStop_HungDaemonFailsNonZero(t *testing.T) {
	suite_provider.NewProvider(t)
	// no daemon.pid in the sandbox: the signal escalation finds nothing to
	// signal and the final port wait is what reports the failure
	switcher.SetWaitTimingsForTest(100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond, 20*time.Millisecond)
	t.Cleanup(func() {
		switcher.SetWaitTimingsForTest(10*time.Second, 2*time.Second, 2*time.Second, 35*time.Second, 200*time.Millisecond)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)

			return
		}
		w.WriteHeader(http.StatusOK) // /admin/stop acknowledged, ignored
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CCX_PORT", strconv.Itoa(listenerPort(t, srv.Listener)))
	var out bytes.Buffer

	code := cli.Execute([]string{"stop"}, &out)

	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "still responding")
}

// A daemon that honors /admin/stop closes the port and the command succeeds.
func TestCommandStop_CleanShutdown(t *testing.T) {
	suite_provider.NewProvider(t)
	stopped := make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/stop" {
			w.WriteHeader(http.StatusOK)
			close(stopped)
			go srv.Close() // the listener must close for the port to free

			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CCX_PORT", strconv.Itoa(listenerPort(t, srv.Listener)))
	var out bytes.Buffer

	code := cli.Execute([]string{"stop"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Daemon stopped")
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon never received /admin/stop")
	}
}

func TestCommandAuth_UnknownProvider(t *testing.T) {
	// a providers.json must exist: without one the init hint answers first
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{
		Name: "z.ai", Protocol: models.ProtocolAnthropic, BaseURL: "https://x",
		Models: []models.Model{{ID: "m", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{"auth", "unknown"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "unknown provider")
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
	port := listenerPort(t, lis)
	require.NoError(t, lis.Close())

	return port
}

// listenerPort extracts an httptest server's port (checked assertion — the
// errcheck/type-assertion policy).
func listenerPort(t *testing.T, lis net.Listener) int {
	t.Helper()
	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return tcpAddr.Port
}
