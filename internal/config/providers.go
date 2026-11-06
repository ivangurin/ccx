package config

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
)

// ErrUnsupportedVersion means providers.json is not v3 (regenerate it with
// ccms init --force).
var ErrUnsupportedVersion = errors.New("config: unsupported providers.json version (run ccms init --force)")

// ProvidersPath is the providers.json path (CCMS_PROVIDERS overrides it).
func (c *Config) ProvidersPath() string {
	if c.ProvidersFile != "" {
		return c.ProvidersFile
	}

	return filepath.Join(c.Home, "providers.json")
}

// SettingsPath is the Claude Code settings.json path (CCMS_SETTINGS overrides it).
func (c *Config) SettingsPath() string {
	if c.SettingsFile != "" {
		return c.SettingsFile
	}

	return filepath.Join(c.Home, "settings.json")
}

// StatePath is the ccms-state.json path (CCMS_STATE overrides it).
func (c *Config) StatePath() string {
	if c.StateFile != "" {
		return c.StateFile
	}

	return filepath.Join(c.Home, "ccms-state.json")
}

// BackupDir is the settings.json backup directory (CCMS_BACKUP_DIR overrides it).
func (c *Config) BackupDir() string {
	if c.BackupDirPath != "" {
		return c.BackupDirPath
	}

	return filepath.Join(c.Home, "backups")
}

// CCMSDir is the ccms data directory: logs, daemon.lock/pid (inside Home).
func (c *Config) CCMSDir() string {
	return filepath.Join(c.Home, "ccms")
}

// LogDir is the daemon log directory (CCMS_LOG_DIR overrides it).
func (c *Config) LogDir() string {
	if c.LogDirPath != "" {
		return c.LogDirPath
	}

	return filepath.Join(c.Home, "ccms", "logs")
}

// LoadProviders reads and validates providers.json v3 (flat profile layout).
func LoadProviders(path string) (*models.Providers, error) {
	var p models.Providers
	if err := jsonfile.ReadJSON(path, &p); err != nil {
		return nil, err
	}
	if p.Version != 3 {
		return nil, fmt.Errorf("%w: found version %d", ErrUnsupportedVersion, p.Version)
	}

	return &p, nil
}

// SaveProviders atomically writes providers.json with 600 permissions (it holds tokens).
func SaveProviders(path string, p *models.Providers) error {
	return jsonfile.WriteAtomicJSON(path, p, 0o600)
}
