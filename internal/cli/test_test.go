package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivangurin/ccx/internal/cli"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// anthropicOK — an Anthropic-compatible upstream answering every probe.
func anthropicOK(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

const (
	cmdTest       = "test"
	flagProvider  = "--provider"
	testModelGLM  = "glm-4.6"
	testModelGood = "good"
)

func TestExecute_Test_Healthy(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicOK(t)
	p := &models.Providers{Providers: models.ProfileMap{}}
	p.Providers.Set("zai", &models.Profile{
		Name: profZaiName, Protocol: models.ProtocolAnthropic,
		BaseURL: up.URL, AuthToken: "s",
		Models: []models.Model{{ID: testModelGLM, Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdTest}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "✓ "+profZai+" [anthropic]")
	require.Contains(t, out.String(), "✓ "+testModelGLM)
	require.Contains(t, out.String(), "1/1 providers ok, 1/1 models ok")
}

func TestExecute_Test_ProviderAndModelErrors(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicOK(t)
	p := &models.Providers{Providers: models.ProfileMap{}}
	p.Providers.Set("ok", &models.Profile{
		Name: "ok", Protocol: models.ProtocolAnthropic,
		BaseURL: up.URL, AuthToken: "s",
		Models: []models.Model{{ID: testModelGood, Default: true}},
	})
	p.Providers.Set("broken", &models.Profile{
		Name: "broken", Protocol: models.ProtocolOpenAI,
		Models: []models.Model{{ID: "gpt", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdTest}, &out)

	require.Equal(t, 1, code)
	s := out.String()
	require.Contains(t, s, "✗ broken")
	require.Contains(t, s, "base_url is empty")
	require.Contains(t, s, "✓ ok [anthropic]")
	require.Contains(t, s, "✓ "+testModelGood)
	// a structurally broken provider hides its models from probing — only
	// the ok provider's model enters the denominator
	require.Contains(t, s, "1/2 providers ok, 1/1 models ok")
}

func TestExecute_Test_MissingProvidersFile(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdTest}, &out)

	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "providers.json")
	require.Contains(t, out.String(), "ccx init")
}

func TestExecute_Test_ProviderFilter(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicOK(t)
	p := &models.Providers{Providers: models.ProfileMap{}}
	p.Providers.Set("a", &models.Profile{
		Name: "a", Protocol: models.ProtocolAnthropic,
		BaseURL: up.URL, AuthToken: "s",
		Models: []models.Model{{ID: "m1", Default: true}},
	})
	p.Providers.Set("b", &models.Profile{
		Name: "b", Protocol: models.ProtocolAnthropic,
		BaseURL: up.URL, AuthToken: "s",
		Models: []models.Model{{ID: "m2", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdTest, flagProvider, "a"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "✓ a [anthropic]")
	require.NotContains(t, out.String(), "✓ b [anthropic]")
}

func TestExecute_Test_UnknownFilter(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicOK(t)
	p := &models.Providers{Providers: models.ProfileMap{}}
	p.Providers.Set("a", &models.Profile{
		Name: "a", Protocol: models.ProtocolAnthropic,
		BaseURL: up.URL, AuthToken: "s",
		Models: []models.Model{{ID: "m1", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdTest, flagProvider, "nope"}, &out)

	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "unknown provider: nope")
}

func TestExecute_Test_UsageError(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := cli.Execute([]string{cmdTest, "junk"}, &out)

	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "usage: ccx test")
}

func TestExecute_Test_ProbeIsRealAnthropicBody(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	p := &models.Providers{Providers: models.ProfileMap{}}
	p.Providers.Set("zai", &models.Profile{
		Name: profZaiName, Protocol: models.ProtocolAnthropic,
		BaseURL: srv.URL, AuthToken: "sekret",
		Models: []models.Model{{ID: testModelGLM, Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	var out bytes.Buffer

	code := cli.Execute([]string{cmdTest}, &out)

	require.Equal(t, 0, code)
	require.Equal(t, "Bearer sekret", auth)
	require.Equal(t, testModelGLM, body["model"])
}
