package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/provider"
)

// runRestore — ccx restore: settings.json comes back from the one-time
// backup (the original pre-ccx settings.json); the daemon is stopped
// best-effort by the switcher (its failure is a note in the output, not an
// error).
func runRestore(_ []string, stdout io.Writer) int {
	p, err := provider.NewEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	out, err := p.GetSwitcherService().RestoreOriginal(context.Background())
	if err != nil {
		fmt.Fprintf(stdout, "%v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, out)

	return 0
}
