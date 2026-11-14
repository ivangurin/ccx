package cli

import (
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

// runList — ccx list: providers and models in file order.
func runList(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	providers, code := loadProvidersOrHint(cfg, stdout)
	if providers == nil {
		return code
	}

	if providers.Providers.Len() == 0 {
		fmt.Fprintln(stdout, "No providers — add them to providers.json or run ccx init")

		return 0
	}

	for _, key := range providers.Providers.Keys {
		prof := providers.Providers.Get(key)
		fmt.Fprintf(stdout, "%s · %s [%s]\n", key, prof.Name, prof.Protocol)
		for _, m := range prof.Models {
			fmt.Fprintf(stdout, "  %s\n", modelLine(m))
		}
	}

	return 0
}

// modelLine — the model's list line: label, id (typed by the user),
// default marker.
func modelLine(m models.Model) string {
	label := m.Label
	if label == "" {
		label = m.ID
	}
	if label != m.ID {
		label += " · " + m.ID
	}
	if m.Default {
		label += " [default]"
	}
	if m.Default {
		return "★ " + label
	}

	return "  " + label
}
