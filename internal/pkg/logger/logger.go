// Package logger — a factory of slog loggers: dev (text, human-readable)
// and json (machine-readable), with the level set by string. A silent
// error-level logger for tests — New(w, "error", "json").
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New builds an slog.Logger for the dev|json format and debug|info|warn|error level.
func New(out io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("logger: unknown level %q", level)
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	switch strings.ToLower(format) {
	case "dev":
		handler = slog.NewTextHandler(out, opts)
	case "json":
		handler = slog.NewJSONHandler(out, opts)
	default:
		return nil, fmt.Errorf("logger: unknown format %q", format)
	}

	return slog.New(handler), nil
}
