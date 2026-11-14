package app_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// e2eProviderKey — the fixture provider key ("zai" is already at the
// package's goconst threshold: the sibling app tests carry its remaining
// occurrences).
const e2eProviderKey = "zai"

// End-to-end: the daemon launched with --provider zai proxies without any
// client token (pass-through; localhost-only surface).
func TestDaemon_EndToEnd_AnthropicPassthrough(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	port := freePort(t)

	// fake upstream of an Anthropic-compatible provider
	upstream := startFakeUpstream(t)

	// suite sandbox (CCX_HOME + CCX_SETTINGS pinned) + providers.json with
	// a live upstream
	providers := &models.Providers{}
	providers.Providers.Set(e2eProviderKey, &models.Profile{
		Name:      "z.ai (fake)",
		Protocol:  models.ProtocolAnthropic,
		BaseURL:   upstream.URL,
		AuthToken: "up-secret",
		Models:    []models.Model{{ID: "glm-5.3[1m]", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), providers))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = app.New(sp.Config, sp.GetLogger(), nil).Run(ctx, app.ServeOptions{Provider: e2eProviderKey, Port: port})
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	require.Eventually(t, func() bool { return alive(base + "/health") }, 5*time.Second, 50*time.Millisecond, "daemon did not come up")

	t.Run("messages route through without a client token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, base+"/v1/messages", strings.NewReader(`{"model":"glm-5.3[1m]","stream":false}`)) //nolint:gosec // test
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.JSONEq(t, `{"content":[{"type":"text","text":"hi"}]}`, string(body))
	})

	t.Run("admin health reports the launch state", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/admin/health", nil) //nolint:gosec // test
		req.Header.Set("X-Ccx", "1")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"zai"`)
		require.Contains(t, string(body), `"anthropic"`)
		require.Contains(t, string(body), strconv.Itoa(port))
	})

	t.Run("models endpoint", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil) //nolint:gosec // test
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func startFakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}]}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func alive(url string) bool {
	resp, err := (&http.Client{Timeout: time.Second}).Get(url) //nolint:gosec // local daemon
	if err != nil {
		return false
	}
	_ = resp.Body.Close()

	return true
}
