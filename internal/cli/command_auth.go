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

// runAuth — ccx auth copilot: GitHub device flow интерактивно.
func runAuth(args []string, stdout io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stdout, "ccx: укажите провайдера: ccx auth copilot")

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
		fmt.Fprintln(stdout, "codex использует логин codex CLI (codex login); ccx читает ~/.codex/auth.json автоматически")

		return 0
	default:
		fmt.Fprintf(stdout, "ccx: неизвестный провайдер %q (copilot | codex)\n", args[1])

		return 1
	}
}

func authCopilot(cfg *config.Config, stdout io.Writer) int {
	store := filepath.Join(cfg.CCXDir(), "copilot-auth.json")
	client := copilot_client.New(store)

	res, err := client.StartDeviceFlow(context.Background())
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, "Откройте в браузере: "+res.VerificationURI)
	fmt.Fprintln(stdout, "Введите код:         "+res.UserCode)
	fmt.Fprintln(stdout, "Ожидаю подтверждение…")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if _, err := client.PollDeviceFlow(ctx, res.DeviceCode); err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintf(stdout, "Готово: токены сохранены в %s\n", store)

	return 0
}
