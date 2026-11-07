package router_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/stretchr/testify/require"
)

// newFixture writes a providers.json into a sandbox: the zai anthropic profile
// (base_url is replaced with a live fake by the tests that need one).
func newFixture(t *testing.T) *suite_provider.Provider {
	t.Helper()
	sp := suite_provider.NewProvider(t)

	p := &models.Providers{
		Daemon: &models.Daemon{Port: 4567},
	}
	p.Providers.Set("zai", &models.Profile{
		Name:      "z.ai",
		Protocol:  models.ProtocolAnthropic,
		BaseURL:   "http://upstream.invalid",
		AuthToken: "s",
		Models:    []models.Model{{ID: "glm", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	return sp
}

func startEchoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"echo":true}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestBuild_ActivatesRoute(t *testing.T) {
	sp := newFixture(t)
	up := startEchoUpstream(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("zai").BaseURL = up.URL
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.NoError(t, err)

	provider, protocol, ok := svc.Active()
	require.True(t, ok)
	require.Equal(t, "zai", provider)
	require.Equal(t, models.ProtocolAnthropic, protocol)
}

func TestBuild_UnknownProviderFails(t *testing.T) {
	sp := newFixture(t)

	_, err := router.Build(context.Background(), sp.Config, sp.Logger(), "nope")
	require.ErrorContains(t, err, "unknown provider")
}

func TestBuild_OpenAIProfileReady(t *testing.T) {
	sp := newFixture(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Set("work", &models.Profile{
		Name:     "OpenAI",
		Protocol: models.ProtocolOpenAI,
		BaseURL:  "https://api.openai.com/v1",
		Models:   []models.Model{{ID: "gpt", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "work")
	require.NoError(t, err)

	provider, protocol, ok := svc.Active()
	require.True(t, ok)
	require.Equal(t, "work", provider)
	require.Equal(t, models.ProtocolOpenAI, protocol)
}

// without ccx auth <key> the translator is not ready — Build fails, the
// daemon exits at startup instead of serving a dead route.
func TestBuild_CopilotWithoutAuth_NotReady(t *testing.T) {
	sp := newFixture(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Set("copilot-x", &models.Profile{Name: "copilot-x", Protocol: models.ProtocolCopilot})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = router.Build(context.Background(), sp.Config, sp.Logger(), "copilot-x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not ready")
}

func TestServeMessages_DelegatesToActive(t *testing.T) {
	sp := newFixture(t)
	up := startEchoUpstream(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("zai").BaseURL = up.URL
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	svc.ServeMessages(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"m":1}`)))

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"echo":true}`, rec.Body.String())

	// count_tokens is delegated to the active translator as well
	rec2 := httptest.NewRecorder()
	svc.ServeCountTokens(rec2, httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusOK, rec2.Code)
}

// CCX_DUMP_DIR captures the raw client body verbatim (unknown fields
// included) and the request still reaches the translator untouched.
func TestServeMessages_DumpsRawBodyWhenEnabled(t *testing.T) {
	sp := newFixture(t)
	up := startEchoUpstream(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("zai").BaseURL = up.URL
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	svc, err := router.Build(context.Background(), sp.Config, sp.Logger(), "zai")
	require.NoError(t, err)

	dir := filepath.Join(t.TempDir(), "dumps")
	t.Setenv("CCX_DUMP_DIR", dir)

	rec := httptest.NewRecorder()
	svc.ServeMessages(rec, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"m":1,"unknown_field":"kept"}`)))

	require.Equal(t, http.StatusOK, rec.Code)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), "anthropic-req")
	dumped, err := os.ReadFile(filepath.Join(dir, entries[0].Name())) //nolint:gosec // test sandbox dir
	require.NoError(t, err)
	require.JSONEq(t, `{"m":1,"unknown_field":"kept"}`, string(dumped))

	// count_tokens gets the same treatment
	rec2 := httptest.NewRecorder()
	svc.ServeCountTokens(rec2, httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"m":2}`)))
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Contains(t, entries[1].Name(), "anthropic-count-tokens")
}

// I6: a hand-edited anthropic/openai profile without base_url — a clear
// error, not a panic on every /v1/messages.
func TestBuild_NoBaseURL_FailsGracefully(t *testing.T) {
	for _, tc := range []struct {
		key      string
		protocol models.Protocol
	}{
		{"bare-anthropic", models.ProtocolAnthropic},
		{"bare-openai", models.ProtocolOpenAI},
	} {
		t.Run(tc.key, func(t *testing.T) {
			sp := newFixture(t)
			p, err := config.LoadProviders(sp.Config.ProvidersPath())
			require.NoError(t, err)
			p.Providers.Set(tc.key, &models.Profile{Name: tc.key, Protocol: tc.protocol}) // no base_url!
			require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

			require.NotPanics(t, func() {
				_, err := router.Build(t.Context(), sp.Config, sp.Logger(), tc.key)
				require.Error(t, err, "a route without base_url must not be built")
				require.Contains(t, err.Error(), "ccx edit")
			})
		})
	}
}

// A codex provider's credentials live in ITS store under <home>/auth/ — a
// valid login there makes the route ready.
func TestBuild_CodexProfile_PersonalStore(t *testing.T) {
	sp := newFixture(t)

	// fake codex login: an access token with a future exp (TokenValid reads the JWT payload)
	payload, err := json.Marshal(map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	jwt := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	// SaveAuth writes the real auth.json shape (tokens nesting, auth_mode)
	require.NoError(t, codex.SaveAuth(sp.Config.AuthStorePath("cx"), codex.Auth{
		AccessToken: jwt, RefreshToken: "r",
	}))

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Set("cx", &models.Profile{Name: "Codex", Protocol: models.ProtocolCodex})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc, err := router.Build(t.Context(), sp.Config, sp.Logger(), "cx")
	require.NoError(t, err)

	provider, _, ok := svc.Active()
	require.True(t, ok)
	require.Equal(t, "cx", provider)
}

// The copilot twin: a fresh copilot_token in the provider's own store is
// ready without any network round-trip.
func TestBuild_CopilotProfile_PersonalStore(t *testing.T) {
	sp := newFixture(t)

	require.NoError(t, jsonfile.WriteAtomicJSON(sp.Config.AuthStorePath("gh"), map[string]string{
		"user_access_token": "u",
		"copilot_token":     "c",
		"expires_at":        time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
	}, 0o600))

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Set("gh", &models.Profile{Name: "GitHub", Protocol: models.ProtocolCopilot})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	svc, err := router.Build(t.Context(), sp.Config, sp.Logger(), "gh")
	require.NoError(t, err)

	provider, _, ok := svc.Active()
	require.True(t, ok)
	require.Equal(t, "gh", provider)
}
