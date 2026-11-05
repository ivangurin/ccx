package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/pkg/logger"
)

// runServe — ccx serve [--daemon]: запуск демона (foreground для отладки;
// --daemon — внутренний режим SpawnDetached, вывод уже в daemon.log).
func runServe(_ []string, _ io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccx: %v\n", err)

		return 1
	}
	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccx: %v\n", err)

		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.New(cfg, log).Run(ctx); err != nil {
		log.Error("демон завершился с ошибкой", "err", err)

		return 1
	}

	return 0
}
