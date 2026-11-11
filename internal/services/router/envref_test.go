// envref tests: Build expands env references ($NAME) in the launched
// profile's credentials — the daemon serves the expanded secret, and a
// missing variable aborts the startup before listening (Ready() semantics:
// /health answering means the route, credentials included, is usable).
package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/stretchr/testify/require"
)

func TestBuild_EnvRefMissing_Fails(t *testing.T) {
	sp := newFixture(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("zai").AuthToken = "$CCX_SERVE_MISSING"
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.ErrorContains(t, err, "CCX_SERVE_MISSING")
}

// The passthrough upstream must see the EXPANDED Bearer, never the literal
// "$NAME" (ccx serve with an anthropic profile).
func TestBuild_EnvRef_ExpandedBearer(t *testing.T) {
	t.Setenv("CCX_SERVE_TOKEN", "sk-env-serve")
	sp := newFixture(t)
	up := bearerUpstream(t, "sk-env-serve")
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	zai := p.Providers.Get("zai")
	zai.BaseURL = up.URL
	zai.AuthToken = "$CCX_SERVE_TOKEN"
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	body := `{"model":"glm","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	svc.ServeMessages(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "upstream saw the expanded Bearer")
}

// bearerUpstream — 200 only for the exact Bearer token; 401 otherwise.
func bearerUpstream(t *testing.T, want string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+want {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"error","error":{"message":"bad bearer"}}`))

			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}
