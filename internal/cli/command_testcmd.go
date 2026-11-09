package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/services/tester"
)

// runTest — ccx test: validate providers.json and probe every provider and
// model with a real request through a temporary daemon. The live view
// repaints in place on a terminal (providers → models, running → ✓/✗ with
// the cause on the line) and degrades to plain appended lines on a pipe;
// exit 1 on any failure.
func runTest(argv []string, stdout io.Writer) int {
	opts, code := parseTestArgs(argv, stdout)
	if code != 0 {
		return code
	}
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}

	// TTY detection on the real stdout (not the injected writer): the
	// repaint escape codes would smear a redirected pipe; tests drive the
	// non-TTY path through cli.Execute and unit-test the TTY frame directly
	tty := false
	if f, ok := stdout.(*os.File); ok {
		info, statErr := f.Stat()
		if statErr == nil && info.Mode()&os.ModeCharDevice != 0 {
			tty = true
		}
	}
	render := tester.NewRenderer(stdout, tty)
	opts.OnUpdate = render.Update

	report := tester.Run(context.Background(), cfg, *opts)
	render.Finish(report)

	if report.ProvidersOK < report.TotalProviders || report.ModelsOK < report.TotalModels {
		return 1
	}

	return 0
}

// parseTestArgs extracts the optional --provider <key> filter; anything else
// is a usage error.
func parseTestArgs(argv []string, stdout io.Writer) (*tester.Options, int) {
	opts := &tester.Options{Version: "test"}
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider", "-p":
			if i+1 >= len(args) {
				fmt.Fprintf(stdout, "ccx: %s requires a provider key\n", args[i])

				return nil, 1
			}
			i++
			opts.Provider = args[i]
		default:
			fmt.Fprintf(stdout, "ccx: unknown argument %q — usage: ccx test [--provider <key>]\n", args[i])

			return nil, 1
		}
	}

	return opts, 0
}
