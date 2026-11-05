package suite_provider_test

import (
	"os"
	"testing"

	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

func TestNewProvider_SandboxHome(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	// sandbox: CCX_HOME points into t.TempDir; the live ~/.claude is unreachable
	require.NotEmpty(t, sp.Config.Home)
	info, err := os.Stat(sp.Config.Home)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.NotContains(t, sp.Config.Home, "/.claude")
}

func TestNewProvider_QuietLogger(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	require.NotNil(t, sp.Logger())
	// error level: ordinary Info is not written (silent test runs)
	sp.Logger().Info("should-not-fail-anything")
}

func TestNewProvider_Context(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	require.NotNil(t, sp.Context())
	require.NoError(t, sp.Context().Err())
}

func TestNewProvider_EnvIsolated(t *testing.T) {
	// a second provider in the same test gets a DIFFERENT sandbox
	sp1 := suite_provider.NewProvider(t)
	sp2 := suite_provider.NewProvider(t)

	require.NotEqual(t, sp1.Config.Home, sp2.Config.Home)
}
