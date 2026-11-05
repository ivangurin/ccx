package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/ivangurin/ccx/internal/config"
)

// runEdit — ccx edit: открыть providers.json в $EDITOR (дефолт vi, как в bash).
func runEdit(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	path := cfg.ProvidersPath()
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stdout, "ccx: %s не найден — сначала ccx init\n", path)

		return 1
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path) //nolint:gosec // EDITOR — осознанный выбор владельца
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stdout, "ccx: редактор завершился с ошибкой: %v\n", err)

		return 1
	}

	return 0
}
