package suite_provider_test

import (
	"context"
	"os"
	"testing"

	codex_client "github.com/ivangurin/ccx/internal/clients/codex"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/mock"
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

	require.NotNil(t, sp.GetLogger())
	// error level: ordinary Info is not written (silent test runs)
	sp.GetLogger().Info("should-not-fail-anything")
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

// The daemon seam is the mock by default; both getters return the same
// instance (lazy singletons).
func TestGetDaemon_MockByDefault(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	d := sp.GetDaemon()
	require.Same(t, sp.GetDaemonMock(), d)
	require.Same(t, sp.GetDaemonMock(), sp.GetDaemon())
}

// GetSwitcher is a lazy singleton — the real production switcher (built over
// GetDaemon, i.e. the mock by default), not a test double.
func TestGetSwitcher_LazySingleton(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	sw := sp.GetSwitcher()
	require.NotNil(t, sw)
	require.Same(t, sw, sp.GetSwitcher())
}

// The codex client seam is the mockery mock by default; the expecter wires
// a token and the call goes through.
func TestGetCodexAuth_MockByDefault(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	m := sp.GetCodexAuthMock()
	require.Same(t, m, sp.GetCodexAuth())
	m.EXPECT().Token(mock.Anything).Return(codex_client.Auth{AccessToken: "at", AccountID: "acc"}, nil).Once()

	auth, err := sp.GetCodexAuth().Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "at", auth.AccessToken)
}

// The copilot client seam mirrors the codex one.
func TestGetCopilotToken_MockByDefault(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	m := sp.GetCopilotTokenMock()
	require.Same(t, m, sp.GetCopilotToken())
	m.EXPECT().Token(mock.Anything).Return("tok", nil).Once()

	tok, err := sp.GetCopilotToken().Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tok", tok)
}
