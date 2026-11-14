package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/provider"
)

// runUse — ccx use <provider> [model] (alias: ccx <provider> [model]).
func runUse(args []string, stdout io.Writer) int {
	if len(args) < 2 || args[1] == "" {
		fmt.Fprintln(stdout, "ccx: specify a provider: ccx use <provider> [model] (see ccx list)")

		return 1
	}
	p, err := provider.NewEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	model := ""
	if len(args) > 2 {
		model = args[2]
	}

	out, err := p.GetSwitcherService().Apply(context.Background(), args[1], model)
	if err != nil {
		fmt.Fprintf(stdout, "%v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, out)

	return 0
}
