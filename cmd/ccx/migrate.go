package main

import (
	"os"
	"path/filepath"

	"github.com/ivangurin/ccx/internal/config"
)

// migrateLegacy is the process-boundary hook (main only — never inside
// cli.Execute, whose tests run bare, without a sandbox): it moves
// pre-rename data from the old world (~/.claude) into the ccx home once,
// best-effort. Errors are swallowed on purpose — a failed copy surfaces
// later as the actionable "providers.json unavailable — run ccx init".
func migrateLegacy() {
	cfg, err := config.NewFromEnv()
	if err != nil {
		return
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return
	}
	// only the real default home migrates: a custom CCX_HOME means a
	// sandboxed or explicitly redirected install and must not pull the
	// live ~/.claude data into itself
	if cfg.Home != filepath.Join(userHome, ".ccx") {
		return
	}

	_ = config.MigrateLegacyData(cfg, filepath.Join(userHome, ".claude"))
}
