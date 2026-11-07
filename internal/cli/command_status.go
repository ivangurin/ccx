package cli

import (
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// runStatus — ccx status: active profile/model from ccx-state.json
// (daemon and drift check will be added in M2, once the daemon itself exists).
func runStatus(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	providers := loadProvidersOrHint(cfg, stdout)
	if providers == nil {
		return 0
	}

	var state models.State
	if err := jsonfile.ReadJSON(cfg.StatePath(), &state); err != nil || state.Profile == "" {
		fmt.Fprintln(stdout, "No active profile — choose one: ccx <profile> [model]")

		return 0
	}

	if prof := providers.Providers.Get(state.Profile); prof == nil {
		fmt.Fprintf(stdout, "Active profile: %s (missing from providers.json — deleted?)\n", state.Profile)
	} else {
		fmt.Fprintf(stdout, "Active profile: %s (%s) · %s\n", state.Profile, prof.Name, prof.Protocol)
	}
	fmt.Fprintf(stdout, "Model:          %s\n", state.Model)
	fmt.Fprintf(stdout, "Updated:        %s\n", state.Updated)

	return 0
}
