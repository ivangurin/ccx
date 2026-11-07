package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/ivangurin/ccx/internal/config"
)

// runEdit — ccx edit: open providers.json in $EDITOR (default vi, as in bash).
func runEdit(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	path := cfg.ProvidersPath()
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stdout, "ccx: %s not found — run ccx init first\n", path)

		return 1
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path) //nolint:gosec // EDITOR is the owner's deliberate choice
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stdout, "ccx: editor exited with error: %v\n", err)

		return 1
	}

	return 0
}
