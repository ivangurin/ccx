package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/services/switcher"
)

// runUse — ccx use <profile> [model] (alias: ccx <profile> [model]).
func runUse(args []string, stdout io.Writer) int {
	if len(args) < 2 || args[1] == "" {
		fmt.Fprintln(stdout, "ccx: specify a profile: ccx use <profile> [model] (see ccx list)")

		return 1
	}
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

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
