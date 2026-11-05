package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/stretchr/testify/require"
)

func newTestRouter(t *testing.T, profiles ...string) (*router.Service, *suite_provider.Provider) {
	t.Helper()
	sp := suite_provider.NewProvider(t)

	p := &models.Providers{
		Version: 2,
		Daemon:  models.Daemon{Port: 4567, AuthToken: "tok"},
	}
	p.Profiles.Set("zai", &models.Profile{
		Name:      "z.ai",
		Protocol:  models.ProtocolAnthropic,
		Anthropic: &models.AnthropicConfig{BaseURL: "http://upstream.invalid", AuthToken: "s"},
		Models:    []models.Model{{ID: "glm", Default: true}},
	})
	for _, key := range profiles {
		protocol := models.Protocol(key)
		if key == "copilot-x" {
			protocol = models.ProtocolCopilot // тест-кейс: без ccx auth copilot
		}
		p.Profiles.Set(key, &models.Profile{Name: key, Protocol: protocol})
	}
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	return router.New(sp.Config, sp.Logger()), sp
}

func TestUse_ActivatesRoute(t *testing.T) {
	svc, sp := newTestRouter(t)
	up := startEchoUpstream(t)

	// подменим base_url на живой фейк
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Profiles.Get("zai").Anthropic.BaseURL = up.URL
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	require.NoError(t, svc.Use(context.Background(), "zai", "glm"))

	profile, model, ok := svc.Active()
	require.True(t, ok)
	require.Equal(t, "zai", profile)
	require.Equal(t, "glm", model)
	require.True(t, svc.Ready())
}

func TestUse_UnknownProfile(t *testing.T) {
	svc, _ := newTestRouter(t)

	err := svc.Use(context.Background(), "nope", "m")
	require.ErrorContains(t, err, "неизвестный профиль")
	_, _, ok := svc.Active()
	require.False(t, ok)
}

func TestUse_OpenAIProfile_Activates(t *testing.T) {
	// openai-профиль: транслятор строится без апстрим-проверки (Ready тривиален)
	svc2, sp := newTestRouter(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Profiles.Set("oa", &models.Profile{
		Name:     "OpenAI",
		Protocol: models.ProtocolOpenAI,
		OpenAI:   &models.OpenAIConfig{BaseURL: "https://api.openai.com/v1"},
		Models:   []models.Model{{ID: "gpt", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	require.NoError(t, svc2.Use(context.Background(), "oa", "gpt"))
	profile, model, ok := svc2.Active()
	require.True(t, ok)
	require.Equal(t, "oa", profile)
	require.Equal(t, "gpt", model)
}

func TestUse_CopilotWithoutAuth_NotReady(t *testing.T) {
	svc, _ := newTestRouter(t, "copilot-x")

	// без ccx auth copilot транслятор не готов — переключение отменяется
	err := svc.Use(context.Background(), "copilot-x", "m")
	require.Error(t, err)
	require.Contains(t, err.Error(), "не готов")
	_, _, ok := svc.Active()
	require.False(t, ok)
}

func TestServeMessages_NoActive_503(t *testing.T) {
	svc, _ := newTestRouter(t)

	rec := httptest.NewRecorder()
	svc.ServeMessages(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`)))

	resp := rec.Result()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Contains(t, rec.Body.String(), "ccx use")
}

func TestServeMessages_DelegatesToActive(t *testing.T) {
	svc, sp := newTestRouter(t)
	up := startEchoUpstream(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Profiles.Get("zai").Anthropic.BaseURL = up.URL
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	require.NoError(t, svc.Use(context.Background(), "zai", "glm"))

	rec := httptest.NewRecorder()
	svc.ServeMessages(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"m":1}`)))

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"echo":true}`, rec.Body.String())
}

func TestUse_SwitchSwapsRoute(t *testing.T) {
	svc, sp := newTestRouter(t, "zai2")
	up := startEchoUpstream(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Profiles.Get("zai").Anthropic.BaseURL = up.URL
	// второй профиль с другим base_url
	p.Profiles.Set("zai2", &models.Profile{
		Name:      "z.ai 2",
		Protocol:  models.ProtocolAnthropic,
		Anthropic: &models.AnthropicConfig{BaseURL: up.URL, AuthToken: "s"},
		Models:    []models.Model{{ID: "glm2", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	require.NoError(t, svc.Use(context.Background(), "zai", "glm"))
	require.NoError(t, svc.Use(context.Background(), "zai2", "glm2"))

	profile, model, _ := svc.Active()
	require.Equal(t, "zai2", profile)
	require.Equal(t, "glm2", model)
}

func TestRestore_FromState(t *testing.T) {
	svc, sp := newTestRouter(t)
	up := startEchoUpstream(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Profiles.Get("zai").Anthropic.BaseURL = up.URL
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	// рестарт демона: state на диске — маршрут восстанавливается
	require.NoError(t, jsonfile.WriteAtomicJSON(sp.Config.StatePath(), models.State{Profile: "zai", Model: "glm"}, 0o600))
	svc.Restore(context.Background())

	profile, model, ok := svc.Active()
	require.True(t, ok)
	require.Equal(t, "zai", profile)
	require.Equal(t, "glm", model)
}

func TestRestore_NoState_NoActive(t *testing.T) {
	svc, _ := newTestRouter(t)
	svc.Restore(context.Background())

	_, _, ok := svc.Active()
	require.False(t, ok)
}

func TestAuthToken(t *testing.T) {
	svc, _ := newTestRouter(t)
	require.Equal(t, "tok", svc.AuthToken())
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
