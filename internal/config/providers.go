package config

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// ErrUnsupportedVersion — файл providers.json не v2 (нужна миграция ccx init).
var ErrUnsupportedVersion = errors.New("config: unsupported providers.json version (run ccx init)")

// ProvidersPath — путь providers.json (CCX_PROVIDERS переопределяет).
func (c *Config) ProvidersPath() string {
	if c.ProvidersFile != "" {
		return c.ProvidersFile
	}

	return filepath.Join(c.Home, "providers.json")
}

// SettingsPath — путь settings.json Claude Code (CCX_SETTINGS переопределяет).
func (c *Config) SettingsPath() string {
	if c.SettingsFile != "" {
		return c.SettingsFile
	}

	return filepath.Join(c.Home, "settings.json")
}

// StatePath — путь ccx-state.json (CCX_STATE переопределяет).
func (c *Config) StatePath() string {
	if c.StateFile != "" {
		return c.StateFile
	}

	return filepath.Join(c.Home, "ccx-state.json")
}

// BackupDir — каталог бэкапов settings.json (CCX_BACKUP_DIR переопределяет).
func (c *Config) BackupDir() string {
	if c.BackupDirPath != "" {
		return c.BackupDirPath
	}

	return filepath.Join(c.Home, "backups")
}

// CCXDir — каталог данных ccx: логи, daemon.lock/pid (внутри Home).
func (c *Config) CCXDir() string {
	return filepath.Join(c.Home, "ccx")
}

// LogDir — каталог логов демона (CCX_LOG_DIR переопределяет).
func (c *Config) LogDir() string {
	if c.LogDirPath != "" {
		return c.LogDirPath
	}

	return filepath.Join(c.Home, "ccx", "logs")
}

// LoadProviders читает и валидирует providers.json v2.
func LoadProviders(path string) (*models.Providers, error) {
	var p models.Providers
	if err := jsonfile.ReadJSON(path, &p); err != nil {
		return nil, err
	}
	if p.Version != 2 {
		return nil, fmt.Errorf("%w: found version %d", ErrUnsupportedVersion, p.Version)
	}

	return &p, nil
}

// SaveProviders атомарно пишет providers.json с правами 600 (там токены).
func SaveProviders(path string, p *models.Providers) error {
	return jsonfile.WriteAtomicJSON(path, p, 0o600)
}
