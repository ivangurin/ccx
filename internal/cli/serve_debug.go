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

// serveLoggers — the pair buildServeLogger hands over: the diagnostics
// logger and the usage logger (two streams over the same sink).
type serveLoggers struct {
	Diagnostics *slog.Logger
	Usage       *slog.Logger
}

// buildServeLogger — the daemon logger. The sink is always the writer given
// (stderr): a spawned daemon's stderr is redirected into
// daemon-<launch time>.log by the parent (SpawnDetached), so the file
// materializes through the redirect, not here — no tee, no second file.
// Level is "debug" with the flag, env-configured otherwise; format is ALWAYS
// env-driven — a debug run stays machine-parseable like every other run.
//
// It returns TWO loggers over the same sink: the diagnostics logger (env
// level/format as above) and the usage logger — usage is DATA, not
// diagnostics, so its stream is pinned to info level and json format
// regardless of the env config: a CCX_LOG_LEVEL=warn daemon must keep
// recording usage, and the jq-able unified-log recipe (kind=usage slices)
// must hold whatever the human-facing format is.
func buildServeLogger(cfg *config.Config, opts app.ServeOptions, stderr io.Writer, _ time.Time) (serveLoggers, func(), error) {
	level := cfg.LogLevel
	if opts.Debug {
		level = "debug"
	}
	log, err := logger.New(stderr, level, cfg.LogFormat)
	if err != nil {
		return serveLoggers{}, nil, fmt.Errorf("logger: %w", err)
	}
	usageLog, err := logger.New(stderr, "info", "json")
	if err != nil {
		return serveLoggers{}, nil, fmt.Errorf("logger: usage stream: %w", err)
	}

	return serveLoggers{Diagnostics: log, Usage: usageLog.With("kind", "usage")}, func() {}, nil
}

// BuildServeLoggerForTest exports buildServeLogger for logger-wiring tests.
var BuildServeLoggerForTest = buildServeLogger
