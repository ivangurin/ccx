package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
)

// runAuth — ccx auth copilot: GitHub device flow, interactive.
func runAuth(args []string, stdout io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stdout, "ccx: specify a provider: ccx auth copilot")

		return 1
	}
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}

	switch args[1] {
	case "copilot":
		return authCopilot(cfg, stdout)
	case "codex":
		fmt.Fprintln(stdout, "codex uses the codex CLI login (codex login); ccx reads ~/.codex/auth.json automatically")

		return 0
	default:
		fmt.Fprintf(stdout, "ccx: unknown provider %q (copilot | codex)\n", args[1])

		return 1
	}
}

func authCopilot(cfg *config.Config, stdout io.Writer) int {
	store := filepath.Join(cfg.Home, "copilot-auth.json")
	client := copilot_client.New(store)

	res, err := client.StartDeviceFlow(context.Background())
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, "Open in browser: "+res.VerificationURI)
	fmt.Fprintln(stdout, "Enter code:      "+res.UserCode)
	fmt.Fprintln(stdout, "Waiting for confirmation...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if _, err := client.PollDeviceFlow(ctx, res.DeviceCode); err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintf(stdout, "Done: tokens saved to %s\n", store)

	return 0
}
