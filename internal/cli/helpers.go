package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

// loadProvidersOrHint reads providers.json; if it is missing or outdated,
// prints a hint and returns nil (exit code 0: bash parity of
// "hint instead of error").
func loadProvidersOrHint(cfg *config.Config, stdout io.Writer) *models.Providers {
	providers, err := config.LoadProviders(cfg.ProvidersPath())
	if err == nil {
		return providers
	}
	if errors.Is(err, config.ErrUnsupportedVersion) {
		fmt.Fprintln(stdout, "providers.json is in the old format (v1) — run ccx init to migrate")

		return nil
	}
	fmt.Fprint(stdout, "ccx: providers.json not found — run ccx init first\n")

	return nil
}
