package cli

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// writeStatusFixture writes providers.json (one provider + optional daemon
// port) and state.json naming that provider active.
func writeStatusFixture(t *testing.T, sp *suite_provider.Provider, prof *models.Profile, daemonPort int) {
	t.Helper()

	p := &models.Providers{}
	if daemonPort > 0 {
		p.Daemon = &models.Daemon{Port: daemonPort}
	}
	p.Providers.Set(prof.Name, prof)
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	require.NoError(t, jsonfile.WriteAtomicJSON(sp.Config.StatePath(), &models.State{
		Provider: prof.Name,
		Model:    "some-model",
		Updated:  "2026-11-09T10:56:00Z",
	}, 0o600))
}

func openAIProfile() *models.Profile {
	return &models.Profile{
		Name:     "x5",
		Protocol: models.ProtocolOpenAI,
		BaseURL:  "https://example.invalid",
		Models:   []models.Model{{ID: "copilot-code-large", Label: "Large", Default: true}},
	}
}

// ccx status never probed the daemon — it showed a healthy "active provider"
// while nothing listened on 4567, which fed the 2026-11-09 misdiagnosis
// ("проверял, что демон запущен"). A translated profile must show daemon
// liveness from /health.
func TestStatus_DaemonDownForTranslatedProfile(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	// an ephemeral FREE port, never the default 4567: the developer's live
	// daemon listens there and the fixture would probe it (the test-suite-
	// as-daemon-killer trap; a down-probe on 4567 answers "up" whenever a
	// live daemon runs)
	writeStatusFixture(t, sp, openAIProfile(), freeStatusPort(t))

	var out bytes.Buffer
	require.Equal(t, 0, runStatus(nil, &out))

	require.Contains(t, out.String(), "Daemon:")
	require.Contains(t, out.String(), "down")
}

func TestStatus_DaemonUpForTranslatedProfile(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(up.Close)
	port := portOf(t, up.URL)
	writeStatusFixture(t, sp, openAIProfile(), port)

	var out bytes.Buffer
	require.Equal(t, 0, runStatus(nil, &out))

	require.Contains(t, out.String(), "Daemon:")
	require.Contains(t, out.String(), "up")
	require.Contains(t, out.String(), strconv.Itoa(port))
}

// A direct anthropic profile uses no daemon by design — "down" there would
// be a false alarm (the 2026-11-07 diagnostic shape: a silent daemon stop is
// expected right after switching to a direct provider).
func TestStatus_DaemonNotUsedForDirectAnthropic(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeStatusFixture(t, sp, &models.Profile{
		Name:     "zai",
		Protocol: models.ProtocolAnthropic,
		BaseURL:  "https://example.invalid",
		Models:   []models.Model{{ID: "glm", Label: "GLM", Default: true}},
	}, 0)

	var out bytes.Buffer
	require.Equal(t, 0, runStatus(nil, &out))

	require.Contains(t, out.String(), "Daemon:")
	require.Contains(t, out.String(), "not used")
}

// freeStatusPort grabs a port nothing is listening on (bind-and-close): the
// down-probe needs a port guaranteed empty — the default 4567 is the live
// daemon's machine-global port and must never appear in a fixture.
func freeStatusPort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port //nolint:errcheck // Addr() has no error to check — errcheck misfires on the assertion chain
	require.NoError(t, l.Close())

	return port
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()

	idx := strings.LastIndex(rawURL, ":")
	require.Positive(t, idx, "url without port: %s", rawURL)
	port, err := strconv.Atoi(rawURL[idx+1:])
	require.NoError(t, err)

	return port
}
