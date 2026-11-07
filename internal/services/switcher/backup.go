package switcher

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// backupSettings — back up settings.json before editing: a umask-077
// equivalent (perms 600), the name carries a timestamp and pid (parity with
// bash-ccx). The backup is the file's exact text — the pristine pre-switch
// copy, byte-for-byte.
func backupSettings(cfg *config.Config, settings *models.Settings) error {
	data := settings.Bytes()
	if err := os.MkdirAll(cfg.BackupDir(), 0o750); err != nil {
		return fmt.Errorf("ccx: backup dir: %w", err)
	}
	name := fmt.Sprintf("settings-%s-%d.json", time.Now().Format("20060102-150405"), os.Getpid())
	path := filepath.Join(cfg.BackupDir(), name)

	return os.WriteFile(path, data, 0o600) //nolint:gosec // fixed perms
}

// writeFileAtomic — tmp in the same directory + rename, perms 600.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".ccxtmp-*")
	if err != nil {
		return fmt.Errorf("tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// writeStateFile — atomic write of ccx-state.json (format 1:1 with bash-ccx).
func writeStateFile(path string, state models.State) error {
	if err := jsonfile.WriteAtomicJSON(path, state, 0o600); err != nil {
		return fmt.Errorf("ccx: writing state: %w", err)
	}

	return nil
}
