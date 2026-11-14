package provider_test

import (
	"testing"

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
	p := provider.New(sp.Config)

	d := p.GetDaemon()
	require.NotNil(t, d)
	require.Same(t, d, p.GetDaemon())
}

// GetSwitcher is a lazy singleton built over GetDaemon.
func TestGetSwitcher_LazySingleton(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := provider.New(sp.Config)

	sw := p.GetSwitcher()
	require.NotNil(t, sw)
	require.Same(t, sw, p.GetSwitcher())
}
