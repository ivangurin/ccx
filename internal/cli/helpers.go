package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

// loadProvidersOrHint reads providers.json and returns (providers, exitCode):
// a missing file keeps the bash parity of "hint instead of error" (0), any
// other failure (corrupt JSON, permissions) prints the real error and yields
// 1 — the init hint there would send the user to `ccx init --force`, which
// replaces the file and destroys their credentials.
func loadProvidersOrHint(cfg *config.Config, stdout io.Writer) (*models.Providers, int) {
	providers, err := config.LoadProviders(cfg.ProvidersPath())
	if err == nil {
		return providers, 0
	}
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprint(stdout, "ccx: providers.json not found — run ccx init first\n")

		return nil, 0
	}
	fmt.Fprintf(stdout, "ccx: providers.json is unreadable: %v\n", err)

	return nil, 1
}
