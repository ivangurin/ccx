package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivangurin/ccms/internal/app"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/pkg/logger"
)

// runServe — ccms serve [--daemon]: run the daemon (foreground for debugging;
// --daemon is SpawnDetached's internal mode, output already goes to daemon.log).
func runServe(_ []string, _ io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccms: %v\n", err)

		return 1
	}
	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccms: %v\n", err)

		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.New(cfg, log).Run(ctx); err != nil {
		log.Error("daemon exited with error", "err", err)

		return 1
	}

	return 0
}
