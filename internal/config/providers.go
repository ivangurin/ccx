package config

import (
	"path/filepath"

	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
)

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

// LoadProviders reads providers.json (unversioned, flat profile layout; a
// legacy "version" field in the file is ignored).
func LoadProviders(path string) (*models.Providers, error) {
	var p models.Providers
	if err := jsonfile.ReadJSON(path, &p); err != nil {
		return nil, err
	}

	return &p, nil
}

// ResolveDaemonPort is the single port-resolution chain for CLI and daemon:
// the --port flag > CCMS_PORT (c.DaemonPort) > providers.json daemon.port >
// DefaultDaemonPort. An unreadable providers.json falls through to the
// default (best-effort: switching still works, on the default port).
func ResolveDaemonPort(c *Config, flagPort int) int {
	if flagPort > 0 {
		return flagPort
	}
	if c.DaemonPort > 0 {
		return c.DaemonPort
	}
	if providers, err := LoadProviders(c.ProvidersPath()); err == nil && providers.DaemonPort() > 0 {
		return providers.DaemonPort()
	}

	return DefaultDaemonPort
}

// SaveProviders atomically writes providers.json with 600 permissions (it holds tokens).
func SaveProviders(path string, p *models.Providers) error {
	return jsonfile.WriteAtomicJSON(path, p, 0o600)
}
