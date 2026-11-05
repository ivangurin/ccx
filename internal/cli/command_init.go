package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/ivangurin/ccms/internal/config"
)

// runInit — ccms init [--force]: create/migrate providers.json.
func runInit(args []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}
	force := len(args) > 1 && args[1] == "--force"

	res, err := config.InitProviders(cfg, force)
	if err != nil {
		if errors.Is(err, config.ErrProvidersExist) {
			// bash-ccx parity: not an error, a hint
			fmt.Fprintf(stdout, "providers.json already exists: %s\n", cfg.ProvidersPath())
			fmt.Fprintln(stdout, "Use 'ccms init --force' to regenerate (the current file will be replaced).")

			return 0
		}
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}

	fmt.Fprintf(stdout, "Created %s (mode 600).\n", res.Path)
	if res.Migrated {
		fmt.Fprintln(stdout, "Migrated v1 → v2 (proxy commands → protocol).")
	}
	fmt.Fprintln(stdout, "Profiles:")
	for _, key := range res.Profiles {
		fmt.Fprintf(stdout, "  %s\n", key)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stdout, "  ⚠ %s\n", w)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Check tokens and model lists: ccms edit")

	return 0
}
