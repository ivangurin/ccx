package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/pkg/logger"
)

// buildServeLogger — the daemon logger. Without --debug: stderr with the
// env-configured level/format. With --debug: level=debug and format=dev
// (the flag beats CCX_LOG_LEVEL/CCX_LOG_FORMAT for this run), teed into
// <LogDir>/debug-<launch time>.log — one file per launch, appended to when
// two launches land in the same second; old files are never cleaned up
// (delete by hand). The returned func closes the file (no-op without the
// flag) — call it when the daemon exits.
func buildServeLogger(cfg *config.Config, opts app.ServeOptions, stderr io.Writer, now time.Time) (*slog.Logger, func(), error) {
	if !opts.Debug {
		log, err := logger.New(stderr, cfg.LogLevel, cfg.LogFormat)
		if err != nil {
			return nil, nil, fmt.Errorf("logger: %w", err)
		}

		return log, func() {}, nil
	}

	file, err := openDebugLog(cfg, now)
	if err != nil {
		return nil, nil, err
	}
	log, err := logger.New(io.MultiWriter(stderr, file), "debug", "dev")
	if err != nil {
		_ = file.Close()

		return nil, nil, fmt.Errorf("logger: %w", err)
	}

	return log, func() { _ = file.Close() }, nil
}

// openDebugLog opens <LogDir>/debug-<YYYYMMDD-HHMMSS>.log (local launch
// time in the name): the debug counterpart of daemon.log, 0600 like every
// ccx-owned file, append — a same-second relaunch shares the file instead
// of clobbering it.
func openDebugLog(cfg *config.Config, now time.Time) (*os.File, error) {
	if err := os.MkdirAll(cfg.LogDir(), 0o750); err != nil {
		return nil, fmt.Errorf("debug log: dir: %w", err)
	}
	path := filepath.Join(cfg.LogDir(), fmt.Sprintf("debug-%s.log", now.Format("20060102-150405")))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // fixed permissions
	if err != nil {
		return nil, fmt.Errorf("debug log: %w", err)
	}

	return file, nil
}

// BuildServeLoggerForTest exports buildServeLogger for logger-wiring tests.
var BuildServeLoggerForTest = buildServeLogger
