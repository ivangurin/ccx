package config_test

import (
	"testing"
	"time"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewFromEnv_Defaults(t *testing.T) {
	t.Setenv("CCMS_HOME", "/tmp/ccms-test-home")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.Equal(t, "/tmp/ccms-test-home", cfg.Home)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "json", cfg.LogFormat)
	require.Equal(t, 0, cfg.DaemonPort)
	require.Equal(t, config.DefaultLockTimeout, cfg.LockTimeout)
	require.Equal(t, config.DefaultDaemonStartTimeout, cfg.DaemonStartTimeout)
}

func TestNewFromEnv_Overrides(t *testing.T) {
	t.Setenv("CCMS_HOME", "/tmp/h")
	t.Setenv("CCMS_LOG_LEVEL", "debug")
	t.Setenv("CCMS_LOG_FORMAT", "dev")
	t.Setenv("CCMS_PORT", "8123")
	t.Setenv("CCMS_LOCK_TIMEOUT", "5s")
	t.Setenv("CCMS_DAEMON_START_TIMEOUT", "3s")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, "dev", cfg.LogFormat)
	require.Equal(t, 8123, cfg.DaemonPort)
	require.Equal(t, 5*time.Second, cfg.LockTimeout)
	require.Equal(t, 3*time.Second, cfg.DaemonStartTimeout)
}

func TestNewFromEnv_TildeHomeExpanded(t *testing.T) {
	t.Setenv("CCMS_HOME", "~/somewhere")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.NotContains(t, cfg.Home, "~")
}

func TestNewFromEnv_DefaultHomeIsDotClaude(t *testing.T) {
	// no CCMS_HOME: home from USER/HOME → ~/.claude (bash-ccx parity)
	t.Setenv("CCMS_HOME", "")

	cfg, err := config.NewFromEnv()
	require.NoError(t, err)
	require.NotEmpty(t, cfg.Home)
}

func TestNewFromEnv_InvalidLogLevel(t *testing.T) {
	t.Setenv("CCMS_HOME", "/tmp/h")
	t.Setenv("CCMS_LOG_LEVEL", "loud")

	_, err := config.NewFromEnv()
	require.Error(t, err)
}

func TestNewFromEnv_InvalidPort(t *testing.T) {
	t.Setenv("CCMS_HOME", "/tmp/h")
	t.Setenv("CCMS_PORT", "70000")

	_, err := config.NewFromEnv()
	require.Error(t, err)
}
