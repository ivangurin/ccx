package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// MigrateLegacyData is the one-time best-effort move of pre-rename data from
// the old world (the ~/.claude root: providers.json, ccms-state.json, and
// the copilot store under its ccms/ subdir) into the ccx home. Copy
// semantics: a file already present at the target wins (the new world is
// authoritative), sources stay in place — deleting user files behind their
// back is not our call; the release notes say what can be cleaned up by
// hand. Disposable history (backups/, logs/) is not migrated.
// A targeted path override (CCX_PROVIDERS/CCX_STATE) opts that file out: an
// explicit redirect means the user manages the location themselves.
func MigrateLegacyData(cfg *Config, legacyDir string) error {
	// the ccx home may not exist yet on a fresh install — create it up front
	if err := os.MkdirAll(cfg.Home, 0o750); err != nil {
		return fmt.Errorf("migrate legacy data: %w", err)
	}

	if cfg.ProvidersFile == "" {
		src := filepath.Join(legacyDir, "providers.json")
		if err := copyIfAbsent(src, cfg.ProvidersPath()); err != nil {
			return err
		}
	}
	if cfg.StateFile == "" {
		// the old name was ccms-state.json — the rename to ccx-state.json
		// happened together with this move, so the legacy file carries it
		src := filepath.Join(legacyDir, "ccms-state.json")
		if err := copyIfAbsent(src, cfg.StatePath()); err != nil {
			return err
		}
	}

	// The old flat copilot store (~/.claude/ccms/copilot-auth.json) is NOT
	// migrated: credentials are per-provider now (<home>/auth/<key>.json,
	// written by ccx auth <key>) and the flat file has no reader.

	return nil
}

// copyIfAbsent copies src to dst unless dst already exists; a missing src is
// not an error (nothing to migrate). 0600: providers.json and the copilot
// store hold tokens.
func copyIfAbsent(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec // fixed migration path
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate legacy data: read %s: %w", src, err)
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("migrate legacy data: stat %s: %w", dst, err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil { //nolint:gosec // fixed permissions
		return fmt.Errorf("migrate legacy data: write %s: %w", dst, err)
	}

	return nil
}
