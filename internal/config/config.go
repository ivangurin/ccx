// Package config is ccx's env configuration (the CCX_* names are inherited
// from the bash original; tests and sandboxes override CCX_HOME). .env is
// deliberately not supported: ccx is a user CLI, not a service with a
// deployment environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v7"
)

// Timeout defaults (ported from bash-ccx: lock 60s — a proxy/daemon may take
// up to 10s to start, we wait in line; daemon start is 10s instead of the
// bash 20s: the Go daemon is ready in milliseconds).
const (
	DefaultLockTimeout        = 60 * time.Second
	DefaultDaemonStartTimeout = 10 * time.Second
	defaultLogLevel           = "info"
	defaultLogFormat          = "json"
	maxPort                   = 65535
)

// Config is the ccx process environment (CLI and daemon read the same one).
type Config struct {
	// Home is the ccx data root (default ~/.ccx): providers.json,
	// ccx-state.json, backups/, logs/, daemon.lock/pid, the copilot store.
	// It is deliberately NOT the Claude Code root: of Claude's ~/.claude
	// only settings.json is touched (edited in place, never moved).
	Home string `env:"CCX_HOME"`

	LogLevel           string        `env:"CCX_LOG_LEVEL"`
	LogFormat          string        `env:"CCX_LOG_FORMAT"`
	DaemonPort         int           `env:"CCX_PORT"`
	LockTimeout        time.Duration `env:"CCX_LOCK_TIMEOUT"`
	DaemonStartTimeout time.Duration `env:"CCX_DAEMON_START_TIMEOUT"`

	// targeted path overrides (CCX_PROVIDERS/... parity with the bash
	// version). backups/ and logs/ have none: they are pinned to <Home>
	// — CCX_HOME already moves the whole data root.
	ProvidersFile string `env:"CCX_PROVIDERS"`
	SettingsFile  string `env:"CCX_SETTINGS"`
	StateFile     string `env:"CCX_STATE"`
}

// NewFromEnv reads CCX_* variables, applies defaults, and validates.
func NewFromEnv() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: parse env: %w", err)
	}
	if cfg.Home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("config: home dir: %w", err)
		}
		cfg.Home = filepath.Join(userHome, ".ccx")
	} else if cfg.Home == "~" || len(cfg.Home) >= 2 && cfg.Home[:2] == "~/" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("config: home dir: %w", err)
		}
		cfg.Home = filepath.Join(userHome, cfg.Home[2:])
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if cfg.LogFormat == "" {
		cfg.LogFormat = defaultLogFormat
	}
	if cfg.LockTimeout == 0 {
		cfg.LockTimeout = DefaultLockTimeout
	}
	if cfg.DaemonStartTimeout == 0 {
		cfg.DaemonStartTimeout = DefaultDaemonStartTimeout
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate fails fast at startup: log level/format and daemon port.
func (c *Config) Validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: invalid CCX_LOG_LEVEL %q", c.LogLevel)
	}
	switch c.LogFormat {
	case "dev", "json":
	default:
		return fmt.Errorf("config: invalid CCX_LOG_FORMAT %q", c.LogFormat)
	}
	// 0 = "port from providers.json daemon.port" — a legitimate value
	if c.DaemonPort < 0 || c.DaemonPort > maxPort {
		return fmt.Errorf("config: invalid CCX_PORT %d", c.DaemonPort)
	}

	return nil
}
