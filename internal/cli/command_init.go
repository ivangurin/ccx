package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/spf13/cobra"
)

// newInitCmd — ccx init [--force] as a cobra command (the flag is bound by
// pflag; the body still takes argv-style args).
func newInitCmd(stdout io.Writer) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "create providers.json (import from settings/env)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			args := []string{"init"}
			if force {
				args = append(args, "--force")
			}

			return toErr(runInit(args, stdout))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "regenerate providers.json (the current file is replaced)")

	return cmd
}

// runInit — ccx init [--force]: create providers.json from the environment.
func runInit(args []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	force := len(args) > 1 && args[1] == "--force"

	res, err := config.InitProviders(cfg, force)
	if err != nil {
		if errors.Is(err, config.ErrProvidersExist) {
			// bash-ccx parity: not an error, a hint
			fmt.Fprintf(stdout, "providers.json already exists: %s\n", cfg.ProvidersPath())
			fmt.Fprintln(stdout, "Use 'ccx init --force' to regenerate (the current file will be replaced).")

			return 0
		}
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}

	fmt.Fprintf(stdout, "Created %s (mode 600).\n", res.Path)
	fmt.Fprintln(stdout, "Profiles:")
	for _, key := range res.Profiles {
		fmt.Fprintf(stdout, "  %s\n", key)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stdout, "  ⚠ %s\n", w)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Check tokens and model lists: ccx edit")

	return 0
}
