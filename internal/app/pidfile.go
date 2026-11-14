package app

import (
	"fmt"
	"os"
	"path/filepath"
)

func (a *App) pidfilePath() string {
	return filepath.Join(a.p.GetConfig().Home, "daemon.pid")
}

func (a *App) writePidfile() {
	_ = os.MkdirAll(a.p.GetConfig().LogDir(), 0o750)
	_ = os.WriteFile(a.pidfilePath(), fmt.Appendf(nil, "%d\n", os.Getpid()), 0o600) //nolint:gosec // fixed permissions
}
