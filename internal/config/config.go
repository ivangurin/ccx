// Package config — env-конфиг ccx (паритет CCX_* с bash-версией: тесты и
// песочницы подменяют CCX_HOME). .env сознательно не поддерживается: ccx —
// CLI пользователя, а не сервис с деплой-окружением.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v7"
)

// Таймауты-дефолты (перенос bash-ccx: lock 60с — прокси/демон может
// стартовать до 10с, ждём очереди; старт демона — 10с вместо bash-20с:
// Go-демон готов за миллисекунды).
const (
	DefaultLockTimeout        = 60 * time.Second
	DefaultDaemonStartTimeout = 10 * time.Second
	defaultLogLevel           = "info"
	defaultLogFormat          = "json"
	maxPort                   = 65535
)

// Config — окружение процесса ccx (CLI и демон читают одно и то же).
type Config struct {
	// Home — корень данных Claude Code (по умолчанию ~/.claude); внутри него
	// providers.json, settings.json, ccx-state.json, backups/, ccx/logs/.
	Home string `env:"CCX_HOME"`

	LogLevel           string        `env:"CCX_LOG_LEVEL"`
	LogFormat          string        `env:"CCX_LOG_FORMAT"`
	DaemonPort         int           `env:"CCX_PORT"`
	LockTimeout        time.Duration `env:"CCX_LOCK_TIMEOUT"`
	DaemonStartTimeout time.Duration `env:"CCX_DAEMON_START_TIMEOUT"`
}

// NewFromEnv читает CCX_* переменные, применяет дефолты и валидирует.
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
		cfg.Home = filepath.Join(userHome, ".claude")
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

// Validate — fail-fast на старте: уровень/формат логов и порт демона.
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
	// 0 = «порт из providers.json daemon.port» — легитимное значение
	if c.DaemonPort < 0 || c.DaemonPort > maxPort {
		return fmt.Errorf("config: invalid CCX_PORT %d", c.DaemonPort)
	}

	return nil
}
