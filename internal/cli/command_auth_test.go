package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// authArg: the literal command word (goconst: it repeats in every call).
const authArg = "auth"

// authFixture: three providers — an authorized copilot, an unauthorized
// codex, an anthropic (auth not supported).
func writeAuthFixture(t *testing.T) *config.Config {
	t.Helper()
	sp := suite_provider.NewProvider(t)

	p := &models.Providers{}
	p.Providers.Set("gh", &models.Profile{Name: "GitHub", Protocol: models.ProtocolCopilot, Models: []models.Model{}})
	p.Providers.Set("cx", &models.Profile{Name: "Codex", Protocol: models.ProtocolCodex, Models: []models.Model{}})
	p.Providers.Set("zai", &models.Profile{
		Name: "z.ai", Protocol: models.ProtocolAnthropic, BaseURL: "https://x",
		Models: []models.Model{{ID: "m", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	require.NoError(t, jsonfile.WriteAtomicJSON(sp.Config.AuthStorePath("gh"),
		map[string]string{"user_access_token": "u"}, 0o600))

	return sp.Config
}

func TestCommandAuth_ListsAuthState(t *testing.T) {
	writeAuthFixture(t)
	var out bytes.Buffer

	code := runAuth([]string{authArg}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "gh (copilot) — authorized")
	require.Contains(t, out.String(), "cx (codex) — not authorized")
	require.Contains(t, out.String(), "zai (anthropic) — auth not supported")
}

func TestCommandAuth_NoArgs_MissingProviders(t *testing.T) {
	suite_provider.NewProvider(t)
	var out bytes.Buffer

	code := runAuth([]string{authArg}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccx init")
}

func TestCommandAuth_UnsupportedProtocol(t *testing.T) {
	writeAuthFixture(t)
	var out bytes.Buffer

	code := runAuth([]string{authArg, "zai"}, &out)

	require.Equal(t, 1, code)
	require.Equal(t, "ccx: provider zai (protocol anthropic) does not support auth\n", out.String())
}

type fakeAuthClient struct{ store string }

func (f *fakeAuthClient) StartDeviceFlow(_ context.Context) (*copilot_client.DeviceFlowResult, error) {
	return &copilot_client.DeviceFlowResult{DeviceCode: "dc", UserCode: "uc", VerificationURI: "https://gh"}, nil
}

func (f *fakeAuthClient) PollDeviceFlow(_ context.Context, _ string) (string, error) {
	if err := jsonfile.WriteAtomicJSON(f.store, map[string]string{"user_access_token": "u"}, 0o600); err != nil {
		return "", err
	}

	return "u", nil
}

func TestCommandAuth_CopilotFlow(t *testing.T) {
	cfg := writeAuthFixture(t)
	var out bytes.Buffer

	var gotStore string
	orig := newCopilotAuthClient
	newCopilotAuthClient = func(store string) copilotAuthClient {
		gotStore = store

		return &fakeAuthClient{store: store}
	}
	t.Cleanup(func() { newCopilotAuthClient = orig })

	code := runAuth([]string{authArg, "gh"}, &out)

	require.Equal(t, 0, code)
	require.Equal(t, filepath.Join(cfg.Home, "auth", "gh.json"), gotStore)
	require.Contains(t, out.String(), "Done: tokens saved to")
}

func TestCommandAuth_CodexFlow(t *testing.T) {
	cfg := writeAuthFixture(t)
	var out bytes.Buffer

	var gotPath string
	orig := codexLogin
	codexLogin = func(_ context.Context, authPath string, _ func(string) error) error {
		gotPath = authPath

		return nil
	}
	t.Cleanup(func() { codexLogin = orig })

	code := runAuth([]string{authArg, "cx"}, &out)

	require.Equal(t, 0, code)
	require.Equal(t, filepath.Join(cfg.Home, "auth", "cx.json"), gotPath)
	require.Contains(t, out.String(), "Done: login saved to")
}

func TestCommandAuth_ExtraArgs(t *testing.T) {
	writeAuthFixture(t)
	var out bytes.Buffer

	code := runAuth([]string{authArg, "gh", "extra"}, &out)

	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "usage")
}
