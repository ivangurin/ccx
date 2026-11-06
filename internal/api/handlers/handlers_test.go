package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccms/internal/api/handlers"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	suite_provider "github.com/ivangurin/ccms/internal/pkg/suite/provider"
	"github.com/ivangurin/ccms/internal/services/router"
	"github.com/stretchr/testify/require"
)

// newTestApp assembles the daemon surface against a live echo upstream: no
// auth middleware — a request without any token must reach the translator.
func newTestApp(t *testing.T) (*httptest.Server, *suite_provider.Provider) {
	t.Helper()
	sp := suite_provider.NewProvider(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"echo":true}`))
	}))
	t.Cleanup(up.Close)

	p := &models.Providers{
		Daemon: models.Daemon{Port: 0},
	}
	p.Profiles.Set("zai", &models.Profile{
		Name:     "z.ai",
		Protocol: models.ProtocolAnthropic,
		BaseURL:  up.URL,
		Models:   []models.Model{{ID: "glm", Label: "GLM", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.NoError(t, err)
	h := handlers.New(handlers.Deps{
		Router:  svc,
		Version: "test",
		Port:    4567,
		Stop:    func() {},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv, sp
}

func TestHealth_NoAuth(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/health") //nolint:gosec // test server
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "json")
}

// No credential on the request — it still reaches the translator (pass-through
// auth: the daemon has no secret of its own to check).
func TestV1Messages_NoAuthHeaderAccepted(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{}`)) //nolint:gosec // test
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAdminUse_Returns404(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Post(srv.URL+"/admin/use", "application/json", strings.NewReader(`{"profile":"zai"}`)) //nolint:gosec // test
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdminReady_Returns404(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/admin/ready") //nolint:gosec // test
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdminHealth_ReportsLaunchState(t *testing.T) {
	srv, _ := newTestApp(t)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/health", nil) //nolint:gosec // test
	req.Header.Set("X-Ccms", "1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var payload struct {
		Version string `json:"version"`
		Ready   bool   `json:"ready"`
		Port    int    `json:"port"`
		Active  struct {
			Provider string `json:"provider"`
			Protocol string `json:"protocol"`
		} `json:"active"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, "test", payload.Version)
	require.True(t, payload.Ready)
	require.Equal(t, 4567, payload.Port)
	require.Equal(t, "zai", payload.Active.Provider)
	require.Equal(t, "anthropic", payload.Active.Protocol)
}

func TestAdminStop_TriggersCallback(t *testing.T) {
	_, sp := newTestApp(t)

	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.NoError(t, err)
	stopped := make(chan struct{})
	h := handlers.New(handlers.Deps{Router: svc, Version: "t", Port: 4567, Stop: func() { close(stopped) }})
	local := httptest.NewServer(h)
	t.Cleanup(local.Close)

	req, _ := http.NewRequest(http.MethodPost, local.URL+"/admin/stop", nil) //nolint:gosec // test
	req.Header.Set("X-Ccms", "1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case <-stopped:
	case <-t.Context().Done():
		t.Fatal("stop callback was not called")
	}
}

func TestModels_ListsActive(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/v1/models") //nolint:gosec // test
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var payload struct {
		Data []map[string]string `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Len(t, payload.Data, 1)
	require.Equal(t, "glm", payload.Data[0]["id"])
}

// A request whose Host is not the loopback origin is rejected (DNS
// rebinding: a public name resolving to 127.0.0.1 must not read responses).
func TestGuard_ForeignHostRejected(t *testing.T) {
	srv, _ := newTestApp(t)

	for _, path := range []string{"/health", "/v1/messages", "/admin/health"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil) //nolint:gosec // test
		req.Host = "evil.example"
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, path)
	}
}

// /admin/* requires the non-simple X-CCMS header: a custom header forces a
// CORS preflight, which a drive-by web page cannot pass silently (the old
// Bearer token used to gate this; pass-through auth removed it).
func TestGuard_AdminWithoutMarkerRejected(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Post(srv.URL+"/admin/stop", "", nil) //nolint:gosec // test
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp2, err := http.Get(srv.URL + "/admin/health") //nolint:gosec // test
	require.NoError(t, err)
	_ = resp2.Body.Close()
	require.Equal(t, http.StatusForbidden, resp2.StatusCode)
}
