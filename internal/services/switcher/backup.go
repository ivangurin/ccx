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

// backupSettings — бэкап settings.json перед правкой: umask-077-эквивалент
// (права 600), имя с таймстампом и pid (паритет bash-ccx).
func backupSettings(cfg *config.Config, settings *models.Settings) error {
	data, err := settings.Marshal()
	if err != nil {
		return fmt.Errorf("ccx: бэкап: %w", err)
	}
	if err := os.MkdirAll(cfg.BackupDir(), 0o750); err != nil {
		return fmt.Errorf("ccx: бэкап-каталог: %w", err)
	}
	name := fmt.Sprintf("settings-%s-%d.json", time.Now().Format("20060102-150405"), os.Getpid())
	path := filepath.Join(cfg.BackupDir(), name)

	return os.WriteFile(path, data, 0o600) //nolint:gosec // фиксированные права
}

// writeFileAtomic — tmp в том же каталоге + rename, права 600.
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

// writeStateFile — атомарная запись ccx-state.json (формат 1:1 bash-ccx).
func writeStateFile(path string, state models.State) error {
	if err := jsonfile.WriteAtomicJSON(path, state, 0o600); err != nil {
		return fmt.Errorf("ccx: запись state: %w", err)
	}

	return nil
}
