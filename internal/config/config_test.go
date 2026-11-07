package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewFromEnv_Defaults(t *testing.T) {
	t.Setenv("CCX_HOME", "/tmp/ccx-test-home")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.Equal(t, "/tmp/ccx-test-home", cfg.Home)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "json", cfg.LogFormat)
	require.Equal(t, 0, cfg.DaemonPort)
	require.Equal(t, config.DefaultLockTimeout, cfg.LockTimeout)
	require.Equal(t, config.DefaultDaemonStartTimeout, cfg.DaemonStartTimeout)
}

func TestNewFromEnv_Overrides(t *testing.T) {
	t.Setenv("CCX_HOME", "/tmp/h")
	t.Setenv("CCX_LOG_LEVEL", "debug")
	t.Setenv("CCX_LOG_FORMAT", "dev")
	t.Setenv("CCX_PORT", "8123")
	t.Setenv("CCX_LOCK_TIMEOUT", "5s")
	t.Setenv("CCX_DAEMON_START_TIMEOUT", "3s")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, "dev", cfg.LogFormat)
	require.Equal(t, 8123, cfg.DaemonPort)
	require.Equal(t, 5*time.Second, cfg.LockTimeout)
	require.Equal(t, 3*time.Second, cfg.DaemonStartTimeout)
}

func TestNewFromEnv_TildeHomeExpanded(t *testing.T) {
	t.Setenv("CCX_HOME", "~/somewhere")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.NotContains(t, cfg.Home, "~")
}

func TestNewFromEnv_DefaultHomeIsDotCcx(t *testing.T) {
	// no CCX_HOME: home from USER/HOME → ~/.ccx — a dedicated ccx data root,
	// separate from Claude Code's ~/.claude (settings.json stays there)
	t.Setenv("CCX_HOME", "")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)

	userHome, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(userHome, ".ccx"), cfg.Home)
}

func TestNewFromEnv_InvalidLogLevel(t *testing.T) {
	t.Setenv("CCX_HOME", "/tmp/h")
	t.Setenv("CCX_LOG_LEVEL", "loud")

	_, err := config.NewFromEnv()
	require.Error(t, err)
}

func TestNewFromEnv_InvalidPort(t *testing.T) {
	t.Setenv("CCX_HOME", "/tmp/h")
	t.Setenv("CCX_PORT", "70000")

	_, err := config.NewFromEnv()
	require.Error(t, err)
}
