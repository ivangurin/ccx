package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/spf13/cobra"
)

// serveFlags — the serve command's pflag-bound launch parameters.
type serveFlags struct {
	provider string
	port     int
	daemon   bool
	debug    bool
}

// bind registers the flags on a cobra command.
func (f *serveFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.provider, "provider", "", "providers.json key to proxy (required)")
	fl.IntVar(&f.port, "port", 0, "listen port (default: CCX_PORT > daemon.port > 4567)")
	fl.BoolVar(&f.daemon, "daemon", false, "spawned mode (output to daemon.log)")
	fl.BoolVar(&f.debug, "debug", false, "debug logging: level=debug, dev format, tee into <CCX_HOME>/logs/debug-<launch time>.log")
}

// options validates the parsed flags and converts them to daemon options.
func (f *serveFlags) options(positional []string) (app.ServeOptions, error) {
	if len(positional) > 0 {
		return app.ServeOptions{}, fmt.Errorf("unexpected argument %q", positional[0])
	}
	if f.provider == "" {
		return app.ServeOptions{}, errors.New("serve requires --provider <key>")
	}
	// the env knob (CCX_PORT) is range-checked in Config.Validate; the flag
	// gets the same bounds — otherwise the failure is an opaque net.Listen
	// bind error after the logger and lock are already up
	if f.port < 0 || f.port > 65535 {
		return app.ServeOptions{}, fmt.Errorf("invalid --port %d: must be within 0-65535 (0 = resolve)", f.port)
	}
	_ = f.daemon // spawn semantics only; behavior does not change in-process

	return app.ServeOptions{Provider: f.provider, Port: f.port, Debug: f.debug}, nil
}

// newServeCmd — ccx serve [--daemon] [--debug] --provider <key> [--port N]:
// run the daemon (foreground for debugging; --daemon is SpawnDetached's
// internal mode, output already goes to daemon.log; --debug = debug/dev
// logging teed into <LogDir>/debug-<launch time>.log). The route comes from
// --provider — there is no in-daemon switching anymore.
func newServeCmd() *cobra.Command {
	var f serveFlags
	cmd := &cobra.Command{
		Use:   "serve --provider <key> [--port N] [--daemon] [--debug]",
		Short: "run the daemon (foreground; spawned by ccx use)",
		Args:  cobra.ArbitraryArgs, // positional check lives in options (pinned message)
		RunE: func(_ *cobra.Command, args []string) error {
			opts, err := f.options(args)
			if err != nil {
				return err
			}

			return toErr(runServe(opts))
		},
	}
	f.bind(cmd)

	return cmd
}

// runServe starts the daemon with validated options.
func runServe(opts app.ServeOptions) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccx: %v\n", err)

		return 1
	}
	log, closeLog, err := buildServeLogger(cfg, opts, os.Stderr, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccx: %v\n", err)

		return 1
	}
	defer closeLog()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.New(cfg, log).Run(ctx, opts); err != nil {
		log.Error("daemon exited with error", "err", err)

		return 1
	}

	return 0
}

// parseServeArgs parses the flags after "serve" through the same cobra flag
// set the command uses (a test seam for flag parsing and validation).
func parseServeArgs(args []string) (app.ServeOptions, error) {
	var f serveFlags
	cmd := &cobra.Command{Use: "serve", SilenceUsage: true, SilenceErrors: true}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	f.bind(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		return app.ServeOptions{}, err
	}

	return f.options(cmd.Flags().Args())
}

// ParseServeForTest exports parseServeArgs for flag-parsing tests.
var ParseServeForTest = parseServeArgs
