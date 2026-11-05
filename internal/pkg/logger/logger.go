// Package logger — фабрика slog-логгеров: dev (text, человеческие читаемые)
// и json (машиночитаемые), уровень задаётся строкой. Тихий error-логгер для
// тестов — New(w, "error", "json").
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// New строит slog.Logger для формата dev|json и уровня debug|info|warn|error.
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
