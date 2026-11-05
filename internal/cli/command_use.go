package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/services/switcher"
)

// runUse — ccms use <profile> [model] (alias: ccms <profile> [model]).
func runUse(args []string, stdout io.Writer) int {
	if len(args) < 2 || args[1] == "" {
		fmt.Fprintln(stdout, "ccms: specify a profile: ccms use <profile> [model] (see ccms list)")

		return 1
	}
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}
	model := ""
	if len(args) > 2 {
		model = args[2]
	}

	sw := switcher.New(cfg, switcher.NewHTTPDaemon(cfg))
	out, err := sw.Apply(context.Background(), args[1], model)
	if err != nil {
		fmt.Fprintf(stdout, "%v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, out)

	return 0
}
