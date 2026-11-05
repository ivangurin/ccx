package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/ivangurin/ccms/internal/config"
)

// runEdit — ccms edit: open providers.json in $EDITOR (default vi, as in bash).
func runEdit(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}
	path := cfg.ProvidersPath()
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stdout, "ccms: %s not found — run ccms init first\n", path)

		return 1
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path) //nolint:gosec // EDITOR is the owner's deliberate choice
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stdout, "ccms: editor exited with error: %v\n", err)

		return 1
	}

	return 0
}
