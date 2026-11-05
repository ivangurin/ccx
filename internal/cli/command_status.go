package cli

import (
	"fmt"
	"io"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
)

// runStatus — ccms status: active profile/model from ccms-state.json
// (daemon and drift check will be added in M2, once the daemon itself exists).
func runStatus(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}
	providers := loadProvidersOrHint(cfg, stdout)
	if providers == nil {
		return 0
	}

	var state models.State
	if err := jsonfile.ReadJSON(cfg.StatePath(), &state); err != nil || state.Profile == "" {
		fmt.Fprintln(stdout, "No active profile — choose one: ccms <profile> [model]")

		return 0
	}

	if prof := providers.Profiles.Get(state.Profile); prof == nil {
		fmt.Fprintf(stdout, "Active profile: %s (missing from providers.json — deleted?)\n", state.Profile)
	} else {
		fmt.Fprintf(stdout, "Active profile: %s (%s) · %s\n", state.Profile, prof.Name, prof.Protocol)
	}
	fmt.Fprintf(stdout, "Model:          %s\n", state.Model)
	fmt.Fprintf(stdout, "Updated:        %s\n", state.Updated)

	return 0
}
