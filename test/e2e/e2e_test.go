package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/logger"
	"github.com/stretchr/testify/require"
)

// freePort — свободный TCP-порт для демона теста.
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

func TestDaemon_EndToEnd_AnthropicPassthrough(t *testing.T) {
	// фейк-апстрим Anthropic-совместимого провайдера
	upstream := startFakeUpstream(t)

	// песочница CCX_HOME + providers.json c живым апстримом
	home := t.TempDir()
	port := freePort(t)
	t.Setenv("CCX_HOME", home)
	t.Setenv("CCX_PORT", strconv.Itoa(port))

	providers := &models.Providers{
		Version: 2,
		Daemon:  models.Daemon{Port: port, AuthToken: "e2e-token"},
	}
	providers.Profiles.Set("zai", &models.Profile{
		Name:      "z.ai (fake)",
		Protocol:  models.ProtocolAnthropic,
		Anthropic: &models.AnthropicConfig{BaseURL: upstream.URL, AuthToken: "up-secret"},
		Models:    []models.Model{{ID: "glm-5.3[1m]", Default: true}},
	})
	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.NoError(t, config.SaveProviders(cfg.ProvidersPath(), providers))

	// state: рестарт-сценарий — маршрут восстанавливается из state
	require.NoError(t, os.WriteFile(cfg.StatePath(), []byte(`{"profile":"zai","model":"glm-5.3[1m]","updated":"now"}`), 0o600))

	log, err := logger.New(os.Stderr, "error", "json")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = app.New(cfg, log).Run(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	require.Eventually(t, func() bool { return alive(base + "/health") }, 5*time.Second, 50*time.Millisecond, "демон не поднялся")

	t.Run("restore from state routes messages", func(t *testing.T) {
		body := postJSON(t, base+"/v1/messages", "e2e-token", `{"model":"glm-5.3[1m]","stream":false}`)
		require.JSONEq(t, `{"content":[{"type":"text","text":"hi"}]}`, body)
	})

	t.Run("unauthorized without token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, base+"/v1/messages", strings.NewReader(`{}`)) //nolint:gosec // тест
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("admin health reports active", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/admin/health", nil) //nolint:gosec // тест
		req.Header.Set("Authorization", "Bearer e2e-token")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"zai"`)
	})

	t.Run("admin use switches", func(t *testing.T) {
		body := postJSON(t, base+"/admin/use", "e2e-token", `{"profile":"zai","model":"glm-5.3[1m]"}`)
		require.Contains(t, body, `"ok"`)
	})

	t.Run("models endpoint", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil) //nolint:gosec // тест
		req.Header.Set("Authorization", "Bearer e2e-token")
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
	resp, err := (&http.Client{Timeout: time.Second}).Get(url) //nolint:gosec // тест
	if err != nil {
		return false
	}
	_ = resp.Body.Close()

	return true
}

func postJSON(t *testing.T, url, token, body string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body)) //nolint:gosec // тест
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return string(data)
}
