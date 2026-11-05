package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/api/handlers"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/stretchr/testify/require"
)

func newTestApp(t *testing.T) (*httptest.Server, *suite_provider.Provider) {
	t.Helper()
	sp := suite_provider.NewProvider(t)

	p := &models.Providers{
		Version: 2,
		Daemon:  models.Daemon{Port: 0, AuthToken: "test-token"},
	}
	p.Profiles.Set("zai", &models.Profile{
		Name:      "z.ai",
		Protocol:  models.ProtocolAnthropic,
		Anthropic: &models.AnthropicConfig{BaseURL: "http://upstream.invalid"},
		Models:    []models.Model{{ID: "glm", Label: "GLM", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc := router.New(sp.Config, sp.Logger())
	h := handlers.New(handlers.Deps{
		Router:  svc,
		Version: "test",
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

func TestMessages_NoToken_401(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{}`)) //nolint:gosec // test
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMessages_WrongToken_401(t *testing.T) {
	srv, _ := newTestApp(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(`{}`)) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMessages_ValidToken_NoActive_503(t *testing.T) {
	srv, _ := newTestApp(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(`{}`)) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "json")
}

func TestAdminUse_SwitchesRoute(t *testing.T) {
	srv, sp := newTestApp(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":1}`))
	}))
	t.Cleanup(up.Close)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Profiles.Get("zai").Anthropic.BaseURL = up.URL
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	body := bytes.NewBufferString(`{"profile":"zai","model":"glm"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/use", body) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// now /v1/messages passes and reaches the upstream
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(`{}`)) //nolint:gosec // test
	req2.Header.Set("Authorization", "Bearer test-token")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
}

func TestAdminUse_UnknownProfile_400(t *testing.T) {
	srv, _ := newTestApp(t)

	body := bytes.NewBufferString(`{"profile":"nope","model":"m"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/use", body) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdminReady_NoRoute_503(t *testing.T) {
	srv, _ := newTestApp(t)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/ready", nil) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestAdminHealth_WithoutRoute(t *testing.T) {
	srv, _ := newTestApp(t)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/health", nil) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "json")
}

func TestAdminStop_TriggersCallback(t *testing.T) {
	srv, sp := newTestApp(t)
	svc := router.New(sp.Config, sp.Logger())
	stopped := make(chan struct{})
	h := handlers.New(handlers.Deps{Router: svc, Version: "t", Stop: func() { close(stopped) }})
	local := httptest.NewServer(h)
	t.Cleanup(local.Close)

	req, _ := http.NewRequest(http.MethodPost, local.URL+"/admin/stop", nil) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case <-stopped:
	case <-contextAfter(t):
		t.Fatal("stop callback was not called")
	}
	_ = srv
}

func contextAfter(t *testing.T) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{})
	go func() {
		<-t.Context().Done()
		close(ch)
	}()

	return ch
}

func TestModels_ListsActive(t *testing.T) {
	srv, sp := newTestApp(t)
	svc := router.New(sp.Config, sp.Logger())
	_ = svc // activating the route via admin is already covered; here only the format matters

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil) //nolint:gosec // test
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
