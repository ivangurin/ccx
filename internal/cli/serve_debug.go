package cli

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/pkg/logger"
)

// buildServeLogger — the daemon logger. The sink is always the writer given
// (stderr): a spawned daemon's stderr is redirected into
// daemon-<launch time>.log by the parent (SpawnDetached), so the file
// materializes through the redirect, not here — no tee, no second file.
// Level is "debug" with the flag, env-configured otherwise; format is ALWAYS
// env-driven — a debug run stays machine-parseable like every other run.
func buildServeLogger(cfg *config.Config, opts app.ServeOptions, stderr io.Writer, _ time.Time) (*slog.Logger, func(), error) {
	level := cfg.LogLevel
	if opts.Debug {
		level = "debug"
	}
	log, err := logger.New(stderr, level, cfg.LogFormat)
	if err != nil {
		return nil, nil, fmt.Errorf("logger: %w", err)
	}

	return log, func() {}, nil
}

// BuildServeLoggerForTest exports buildServeLogger for logger-wiring tests.
var BuildServeLoggerForTest = buildServeLogger
