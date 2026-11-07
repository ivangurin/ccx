package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// ProvidersPath is the providers.json path (CCX_PROVIDERS overrides it).
func (c *Config) ProvidersPath() string {
	if c.ProvidersFile != "" {
		return c.ProvidersFile
	}

	return filepath.Join(c.Home, "providers.json")
}

// SettingsPath is the Claude Code settings.json path (CCX_SETTINGS overrides
// it). The default deliberately lives in Claude Code's own root (~/.claude),
// outside the ccx home: settings.json belongs to Claude Code, ccx only edits
// it in place — it never moves with the data.
func (c *Config) SettingsPath() string {
	if c.SettingsFile != "" {
		return c.SettingsFile
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		// unreachable in practice: NewFromEnv fails fast on a broken HOME
		return filepath.Join(".claude", "settings.json")
	}

	return filepath.Join(userHome, ".claude", "settings.json")
}

// StatePath is the ccx-state.json path (CCX_STATE overrides it).
func (c *Config) StatePath() string {
	if c.StateFile != "" {
		return c.StateFile
	}

	return filepath.Join(c.Home, "ccx-state.json")
}

// BackupDir is the settings.json backup directory, pinned to <Home>/backups
// (there is no env override: CCX_HOME already moves the whole data root).
func (c *Config) BackupDir() string {
	return filepath.Join(c.Home, "backups")
}

// LogDir is the daemon log directory, pinned to <Home>/logs (there is no
// env override: CCX_HOME already moves the whole data root).
func (c *Config) LogDir() string {
	return filepath.Join(c.Home, "logs")
}

// AuthStorePath is the per-provider credential store:
// <CCX_HOME>/auth/<provider-key>.json. The key also names the file, which is
// why LoadProviders rejects keys that are not filename-safe — a key with a
// separator would escape the auth directory. One path policy for the auth
// command, the daemon route and the status quota panel.
func (c *Config) AuthStorePath(providerKey string) string {
	return filepath.Join(c.Home, "auth", providerKey+".json")
}

// LoadProviders reads providers.json (unversioned; the map key is
// "providers", the legacy "profiles" key keeps loading forever). A file
// still carrying pre-rename residue (version, daemon.auth_token, the
// "profiles" key) is rewritten in the current schema on the spot — see
// providers_migrate.go.
func LoadProviders(path string) (*models.Providers, error) {
	data, err := jsonfile.ReadRaw(path)
	if err != nil {
		return nil, err
	}

	var p models.Providers
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("jsonfile: unmarshal %s: %w", path, err)
	}

	for _, key := range p.Providers.Keys {
		if !validProviderKey(key) {
			return nil, fmt.Errorf("providers: provider key %q is not usable as a file name (it names the auth store file)", key)
		}
	}

	// Detection runs only after a successful decode: a broken file must
	// surface the unmarshal error, never a migration attempt.
	if hasLegacyProvidersResidue(data) {
		migrateProvidersFile(path, &p)
	}

	return &p, nil
}

// validProviderKey: the provider key doubles as a file name under <home>/auth,
// so it must stay inside that directory — no separators, no dot navigation,
// no empty name (an empty key is valid JSON but names nothing).
func validProviderKey(key string) bool {
	if key == "" || key == "." || key == ".." {
		return false
	}

	return !strings.ContainsAny(key, `/\`)
}

// ResolveDaemonPort is the single port-resolution chain for CLI and daemon:
// the --port flag > CCX_PORT (c.DaemonPort) > providers.json daemon.port >
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
