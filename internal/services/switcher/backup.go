package switcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// backupSettings — freeze the original settings.json before the first edit:
// a umask-077 equivalent (perms 600). The backup is the file's exact text —
// the pristine pre-switch copy, byte-for-byte. Created exactly once (the
// name is fixed, O_EXCL makes "already exists" an atomic skip): every later
// switch finds the file on disk and leaves the original frozen — ccx never
// accumulates backups.
//
// Known limit for installs that lived through the old accumulating scheme
// (≤ v0.7.0, ~/.ccx/backups/settings-*.json): their first post-upgrade
// switch freezes an ALREADY ccx-managed file, not the pristine pre-ccx
// original — the true original sits as the oldest file in the legacy
// backups/ dir, and nothing imports it.
func backupSettings(cfg *config.Config, settings *models.Settings) error {
	f, err := os.OpenFile(cfg.SettingsBackupPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // fixed perms
	if errors.Is(err, fs.ErrExist) {
		return nil //nolint:nilerr // the original is already frozen — skip
	}
	if err != nil {
		return fmt.Errorf("ccx: backup: %w", err)
	}
	if _, err := f.Write(settings.Bytes()); err != nil {
		_ = f.Close()
		// A partial one-time backup must not survive: the ErrExist skip would
		// keep treating the truncated file as the frozen original forever —
		// the pristine copy would never be captured. Best-effort: the switch
		// is already failing.
		_ = os.Remove(cfg.SettingsBackupPath())

		return fmt.Errorf("ccx: backup: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(cfg.SettingsBackupPath()) // same partial-backup cleanup as above

		return fmt.Errorf("ccx: backup: %w", err)
	}

	return nil
}

// RestoreOriginal — ccx restore: settings.json comes back from the one-time
// backup (the pre-ccx original) and the daemon — pointless without ccx's env
// block in settings.json — is stopped best-effort (a failure to stop must
// not fail the already-committed restore). Runs under the switcher's flock
// so it cannot interleave with a live switch.
//
// The backup is whatever the first switch froze: for installs upgrading from
// the old accumulating scheme (≤ v0.7.0) that is an already-ccx-managed
// settings.json, not the pristine original (the true one sits as the oldest
// file in ~/.ccx/backups — nothing imports it).
//
// state.json is reset to an empty provider (the file stays — its readers,
// incl. the user's statusline jq, expect it to exist): after a restore NO
// provider is on record, and a stale record would lie — rollbackDaemon would
// respawn a provider the restored settings.json no longer names and ccx
// status would report it active, making "you stay on the previous provider"
// a lie.
func (s *Switcher) RestoreOriginal(ctx context.Context) (string, error) {
	return s.underSwitchLock(func() (string, error) {
		return s.restoreOriginalLocked(ctx)
	})
}

func (s *Switcher) restoreOriginalLocked(ctx context.Context) (string, error) {
	data, err := jsonfile.ReadRaw(s.cfg.SettingsBackupPath())
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("ccx: backup not found at %s — nothing to restore (no switch has frozen the original yet)", s.cfg.SettingsBackupPath())
	}
	if err != nil {
		return "", fmt.Errorf("ccx: reading backup: %w", err)
	}
	// The backup is the only bytes ever copied over settings.json without a
	// re-read through models.LoadSettings (which validates on load) — guard
	// here: restoring a hand-corrupted or crash-leftover file would leave
	// Claude Code with a config every later switch refuses to load.
	if !json.Valid(data) {
		return "", fmt.Errorf("ccx: backup at %s is not valid JSON — refusing to restore it over settings.json", s.cfg.SettingsBackupPath())
	}

	// WriteAtomicRaw (not a plain copy): settings.json belongs to Claude
	// Code — the file's existing perms are kept, a missing file is created
	// with 0600.
	if err := jsonfile.WriteAtomicRaw(s.cfg.SettingsPath(), data, 0o600); err != nil {
		return "", fmt.Errorf("ccx: writing settings.json: %w", err)
	}

	out := fmt.Sprintf("Restored: %s (the original pre-ccx settings.json)", s.cfg.SettingsPath())
	// Best-effort like the daemon stop below — the restore is already
	// committed, a failed reset must not fail it; but it must be visible:
	// an unnoticed stale record is exactly what this reset prevents.
	if err := writeStateFile(s.cfg.StatePath(), models.State{Updated: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		out += fmt.Sprintf("\nnote: state.json was not reset: %v", err)
	}
	if err := s.daemon.Stop(ctx); err != nil {
		out += fmt.Sprintf("\nnote: the daemon is still running: %v (ccx stop)", err)
	}

	return out, nil
}

// writeStateFile — atomic write of state.json (format 1:1 with bash-ccx).
func writeStateFile(path string, state models.State) error {
	if err := jsonfile.WriteAtomicJSON(path, state, 0o600); err != nil {
		return fmt.Errorf("ccx: writing state: %w", err)
	}

	return nil
}
