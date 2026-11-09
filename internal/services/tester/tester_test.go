package tester_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/tester"
	"github.com/stretchr/testify/require"
)

const (
	probeModelID = "glm-4.6"
	testVersion  = "test"
)

// anthropicUpstream — an Anthropic-compatible upstream: a hi probe gets a
// minimal non-stream message back; the fail mode ("" answers 200) decides
// the probe result per call.
func anthropicUpstream(t *testing.T, failModel string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == failModel {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"model not found"}}`))

			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"` + req.Model + `","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// writeProviders saves a providers.json into the sandbox.
func writeProviders(t *testing.T, sp *suite_provider.Provider, p *models.Providers) {
	t.Helper()
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
}

// newProviders — an empty providers root; chain .With to add profiles.
func newProviders() *providersBuilder {
	return &providersBuilder{p: &models.Providers{Providers: models.ProfileMap{}}}
}

type providersBuilder struct {
	p *models.Providers
}

func (b *providersBuilder) With(key string, prof *models.Profile) *providersBuilder {
	b.p.Providers.Set(key, prof)

	return b
}

// done unwraps the builder for SaveProviders.
func (b *providersBuilder) done() *models.Providers { return b.p }

func anthropicProfile(baseURL string, ms ...models.Model) *models.Profile {
	return &models.Profile{
		Name:      "z.ai",
		Protocol:  models.ProtocolAnthropic,
		BaseURL:   baseURL,
		AuthToken: "secret",
		Models:    ms,
	}
}

func TestTest_AllHealthy(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicUpstream(t, "")
	writeProviders(t, sp, newProviders().With("zai", anthropicProfile(up.URL,
		models.Model{ID: probeModelID, Default: true},
		models.Model{ID: "glm-4-air"},
	)).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	require.Len(t, report.Providers, 1)
	res := report.Providers[0]
	require.Equal(t, "zai", res.Key)
	require.NoError(t, res.Err)
	require.Len(t, res.Models, 2)
	require.NoError(t, res.Models[0].Err)
	require.NoError(t, res.Models[1].Err)
	require.Equal(t, 1, report.ProvidersOK)
	require.Equal(t, 2, report.ModelsOK)
}

func TestTest_ProviderError_Structural(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp, newProviders().With("broken", &models.Profile{
		Name:     "Broken",
		Protocol: models.ProtocolOpenAI,
		// no base_url — the structural error
		Models: []models.Model{{ID: "gpt", Default: true}},
	}).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	res := report.Providers[0]
	require.Equal(t, "broken", res.Key)
	require.ErrorContains(t, res.Err, "base_url")
	// models are not probed on a broken provider
	require.Empty(t, res.Models)
	require.Equal(t, 0, report.ProvidersOK)
}

func TestTest_ProviderError_UnknownProtocol(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp, newProviders().With("weird", &models.Profile{
		Name:     "Weird",
		Protocol: models.Protocol("mistral"),
		Models:   []models.Model{{ID: "m", Default: true}},
	}).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	require.ErrorContains(t, report.Providers[0].Err, "unknown protocol")
}

func TestTest_ProviderError_StructuralModels(t *testing.T) {
	t.Run("no id", func(t *testing.T) {
		sp := suite_provider.NewProvider(t)
		up := anthropicUpstream(t, "")
		writeProviders(t, sp, newProviders().With("zai", anthropicProfile(up.URL,
			models.Model{ID: "", Default: true},
		)).done())

		report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

		require.ErrorContains(t, report.Providers[0].Err, "no id")
	})
	t.Run("duplicate id", func(t *testing.T) {
		sp := suite_provider.NewProvider(t)
		up := anthropicUpstream(t, "")
		writeProviders(t, sp, newProviders().With("zai", anthropicProfile(up.URL,
			models.Model{ID: probeModelID},
			models.Model{ID: probeModelID},
		)).done())

		report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

		require.ErrorContains(t, report.Providers[0].Err, "duplicate")
	})
}

func TestTest_ModelError(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicUpstream(t, "glm-4-air")
	writeProviders(t, sp, newProviders().With("zai", anthropicProfile(up.URL,
		models.Model{ID: probeModelID, Default: true},
		models.Model{ID: "glm-4-air"},
	)).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	res := report.Providers[0]
	require.NoError(t, res.Err)
	require.NoError(t, res.Models[0].Err)
	require.ErrorContains(t, res.Models[1].Err, "400")
	require.Contains(t, res.Models[1].Err.Error(), "model not found")
	require.Equal(t, 1, report.ProvidersOK)
	require.Equal(t, 1, report.ModelsOK)
}

func TestTest_UnreachableUpstream(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeProviders(t, sp, newProviders().With("zai", anthropicProfile("http://127.0.0.1:1",
		models.Model{ID: probeModelID, Default: true},
	)).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	res := report.Providers[0]
	// provider itself is structurally fine — the failure is on the model probe
	require.NoError(t, res.Err)
	require.Len(t, res.Models, 1)
	require.Error(t, res.Models[0].Err)
}

func TestTest_ProviderFilter(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicUpstream(t, "")
	p := &models.Providers{Providers: models.ProfileMap{}}
	p.Providers.Set("zai", anthropicProfile(up.URL, models.Model{ID: probeModelID, Default: true}))
	p.Providers.Set("other", anthropicProfile(up.URL, models.Model{ID: "m", Default: true}))
	writeProviders(t, sp, p)

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Provider: "zai"})

	require.Len(t, report.Providers, 1)
	require.Equal(t, "zai", report.Providers[0].Key)
}

func TestTest_ProviderFilter_Unknown(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicUpstream(t, "")
	writeProviders(t, sp, newProviders().With("zai", anthropicProfile(up.URL, models.Model{ID: probeModelID, Default: true})).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Provider: "nope"})

	require.Len(t, report.Providers, 1)
	require.ErrorContains(t, report.Providers[0].Err, "unknown provider")
}

func TestTest_ProbeCarriesCredential(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	writeProviders(t, sp, newProviders().With("zai", anthropicProfile(srv.URL, models.Model{ID: probeModelID, Default: true})).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	require.NoError(t, report.Providers[0].Models[0].Err)
	require.Equal(t, "Bearer secret", gotAuth)
}

func TestTest_ProbeBody(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	writeProviders(t, sp, newProviders().With("zai", anthropicProfile(srv.URL, models.Model{ID: probeModelID, Default: true})).done())

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	require.NoError(t, report.Providers[0].Models[0].Err)
	require.Equal(t, probeModelID, body["model"])
	require.InEpsilon(t, 16, body["max_tokens"], 0.001)
	msgs, ok := body["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 1)
	first, ok := msgs[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user", first["role"])
}

func TestTest_ReportCounts(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicUpstream(t, "glm-4-air")
	p := &models.Providers{Providers: models.ProfileMap{}}
	p.Providers.Set("zai", anthropicProfile(up.URL,
		models.Model{ID: probeModelID, Default: true},
		models.Model{ID: "glm-4-air"},
	))
	p.Providers.Set("broken", &models.Profile{
		Name: "Broken", Protocol: models.ProtocolOpenAI,
		Models: []models.Model{{ID: "gpt", Default: true}},
	})
	writeProviders(t, sp, p)

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	require.Equal(t, 2, report.TotalProviders)
	require.Equal(t, 1, report.ProvidersOK)
	// the broken provider's model counts toward the total but never passes
	require.Equal(t, 3, report.TotalModels)
	require.Equal(t, 1, report.ModelsOK)
}

func TestTest_NoProvidersFile(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	_ = os.Remove(filepath.Join(sp.Config.Home, "providers.json"))

	report := tester.Run(sp.Context(), sp.Config, tester.Options{Version: testVersion})

	require.Len(t, report.Providers, 1)
	require.ErrorContains(t, report.Providers[0].Err, "providers.json")
	require.Contains(t, report.Providers[0].Err.Error(), "init")
}
