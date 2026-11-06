package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivangurin/ccms/internal/app"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/pkg/logger"
)

// runServe — ccms serve [--daemon] --provider <key> [--port N]: run the daemon
// (foreground for debugging; --daemon is SpawnDetached's internal mode, output
// already goes to daemon.log). The route comes from --provider — there is no
// in-daemon switching anymore.
func runServe(args []string, stdout io.Writer) int {
	opts, err := parseServeArgs(args[1:])
	if err != nil {
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}

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

	if err := app.New(cfg, log).Run(ctx, opts); err != nil {
		log.Error("daemon exited with error", "err", err)

		return 1
	}

	return 0
}

// parseServeArgs parses the flags after "serve".
func parseServeArgs(args []string) (app.ServeOptions, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // runServe prints the error itself; no double usage dump
	provider := fs.String("provider", "", "providers.json key to proxy (required)")
	port := fs.Int("port", 0, "listen port (default: CCMS_PORT > daemon.port > 4567)")
	daemon := fs.Bool("daemon", false, "spawned mode (output to daemon.log)")
	if err := fs.Parse(args); err != nil {
		return app.ServeOptions{}, err
	}
	if fs.NArg() > 0 {
		return app.ServeOptions{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *provider == "" {
		return app.ServeOptions{}, errors.New("serve requires --provider <key>")
	}
	_ = daemon // spawn semantics only; behavior does not change in-process

	return app.ServeOptions{Provider: *provider, Port: *port}, nil
}

// ParseServeForTest exports parseServeArgs for flag-parsing tests.
var ParseServeForTest = parseServeArgs
