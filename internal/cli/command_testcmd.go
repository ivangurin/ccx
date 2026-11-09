package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/services/tester"
)

// runTest — ccx test: validate providers.json and probe every provider and
// model with a real request through a temporary daemon. Output is a
// three-level list: provider → models → model errors; exit 1 on any failure.
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

	report := tester.Run(context.Background(), cfg, *opts)

	fmt.Fprintln(stdout, "Checking providers.json")
	for _, p := range report.Providers {
		if p.Err != nil {
			fmt.Fprintf(stdout, "  ✗ %s — %v\n", p.Key, p.Err)

			continue
		}
		fmt.Fprintf(stdout, "  ✓ %s [%s]\n", p.Key, p.Protocol)
		for _, m := range p.Models {
			if m.Err != nil {
				fmt.Fprintf(stdout, "    ✗ %s\n      ✗ %v\n", m.ID, m.Err)
			} else {
				fmt.Fprintf(stdout, "    ✓ %s\n", m.ID)
			}
		}
	}
	fmt.Fprintf(stdout, "\n%d/%d providers ok, %d/%d models ok\n",
		report.ProvidersOK, report.TotalProviders, report.ModelsOK, report.TotalModels)

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
