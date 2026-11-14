package provider_test

import (
	"context"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/provider"
	"github.com/stretchr/testify/require"
)

// NewEnv resolves the config from the environment: under the suite sandbox
// it lands on the sandbox home, not the live ~/.ccx.
func TestNewEnv_BuildsOverSandboxHome(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	p, err := provider.NewEnv()
	require.NoError(t, err)
	require.Equal(t, sp.Config.Home, p.GetConfig().Home)
}

// GetDaemon is a lazy singleton: both calls return the same instance.
func TestGetDaemon_LazySingleton(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := provider.New(sp.Config, nil)

	d := p.GetDaemon()
	require.NotNil(t, d)
	require.Same(t, d, p.GetDaemon())
}

// GetSwitcher is a lazy singleton built over GetDaemon.
func TestGetSwitcher_LazySingleton(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := provider.New(sp.Config, nil)

	sw := p.GetSwitcher()
	require.NotNil(t, sw)
	require.Same(t, sw, p.GetSwitcher())
}

// GetRoute builds the daemon's single route over the sandbox (an anthropic
// profile needs no upstream for Ready) and pins the slot: the second call
// with the same key returns the same service, a different key errors — one
// provider per daemon process.
func TestGetRoute_SingleSlot(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{
		Protocol: models.ProtocolAnthropic,
		BaseURL:  "https://api.z.ai/api/anthropic",
		Models:   []models.Model{{ID: "glm", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	prv := provider.New(sp.Config, nil)
	svc, err := prv.GetRoute(context.Background(), "zai")
	require.NoError(t, err)
	require.NotNil(t, svc)

	same, err := prv.GetRoute(context.Background(), "zai")
	require.NoError(t, err)
	require.Same(t, svc, same)

	_, err = prv.GetRoute(context.Background(), "acme")
	require.ErrorContains(t, err, "one provider per daemon process")
}

// GetTester is a lazy singleton built over the provider's config.
func TestGetTester_LazySingleton(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := provider.New(sp.Config, nil)

	tr := p.GetTester()
	require.NotNil(t, tr)
	require.Same(t, tr, p.GetTester())
}
