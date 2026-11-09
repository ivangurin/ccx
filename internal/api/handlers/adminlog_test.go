package handlers_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/api/handlers"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/stretchr/testify/require"
)

// /admin/stop is the only fully silent way the daemon exits — during the
// 2026-11-09 ECONNREFUSED hunt nothing said WHO stopped it. Every admin
// request must leave an access line (method, path, remote) in the daemon
// log so a stray local stopper identifies itself.
func TestAdmin_AccessLogged(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"echo":true}`))
	}))
	t.Cleanup(up.Close)

	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{
		Name:     "z.ai",
		Protocol: models.ProtocolAnthropic,
		BaseURL:  up.URL,
		Models:   []models.Model{{ID: "glm", Label: "GLM", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.NoError(t, err)

	var buf bytes.Buffer
	stopped := make(chan struct{})
	h := handlers.New(handlers.Deps{
		Router:  svc,
		Version: "test",
		Port:    4567,
		Log:     slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Stop:    func() { close(stopped) },
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/stop", nil)
	req.Header.Set("X-Ccx", "1")
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Contains(t, buf.String(), "admin request")
	require.Contains(t, buf.String(), "POST")
	require.Contains(t, buf.String(), "/admin/stop")
	require.Contains(t, buf.String(), "remote", "the caller's address must be logged")

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop was not called")
	}
}
