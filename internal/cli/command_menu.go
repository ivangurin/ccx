package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/ivangurin/ccx/internal/pkg/tui"
)

// runMenu — ccx with no arguments: provider → model → use. A single staged
// TUI session: the model list replaces the provider list in place, Esc on
// the models returns to the providers, Esc on the providers exits.
func runMenu(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	providers := loadProvidersOrHint(cfg, stdout)
	if providers == nil {
		return 0
	}
	if providers.Providers.Len() == 0 {
		fmt.Fprintln(stdout, "No providers — ccx init or ccx edit")

		return 0
	}

	defProfile := activeProfileIndex(cfg, providers)
	next := menuStages(providers, defProfile)
	chosen, err := tui.SelectStaged(next, os.Stdin, stdout)
	if err != nil || len(chosen) == 0 {
		return 0
	}

	profileKey := providers.Providers.Keys[chosen[0]]
	profile := providers.Providers.Get(profileKey)
	if len(profile.Models) == 0 {
		// the chain stopped at the provider: its model level was never built
		fmt.Fprintf(stdout, "ccx: provider %s has no models — ccx edit\n", profileKey)

		return 1
	}
	model := profile.Models[chosen[1]].ID

	fmt.Fprint(stdout, "\n")

	return runUse([]string{"use", profileKey, model}, stdout)
}

// menuStages — the menu levels: providers, then the chosen provider's models.
// The builder must return ok=false once every level has a choice — a staged
// session finishes only on ok=false, otherwise each Enter pushes one more
// level (the model menu would reopen instead of applying the selection).
// A provider without models ends the chain early — runMenu reports it.
func menuStages(providers *models.Providers, defProfile int) tui.NextStage {
	return func(chosen []int) (tui.Stage, bool) {
		switch len(chosen) {
		case 0:
			return tui.Stage{
				Header:     "Choose a provider",
				Items:      profileMenuItems(providers),
				DefaultIdx: defProfile,
			}, true
		case 1:
			profileKey := providers.Providers.Keys[chosen[0]]
			profile := providers.Providers.Get(profileKey)
			if len(profile.Models) == 0 {
				return tui.Stage{}, false
			}

			return tui.Stage{
				Header:     "Choose a model · " + profileKey,
				Items:      modelMenuItems(profile),
				DefaultIdx: defaultModelIndex(profile),
			}, true
		default:
			return tui.Stage{}, false
		}
	}
}

// profileMenuItems — profile menu entries in file order.
func profileMenuItems(providers *models.Providers) []tui.Item {
	items := make([]tui.Item, 0, providers.Providers.Len())
	for _, key := range providers.Providers.Keys {
		items = append(items, tui.Item{Label: key, Detail: providers.Providers.Get(key).Name})
	}

	return items
}

// modelMenuItems — the profile's model menu entries.
func modelMenuItems(profile *models.Profile) []tui.Item {
	items := make([]tui.Item, 0, len(profile.Models))
	for _, m := range profile.Models {
		label := m.Label
		if label == "" {
			label = m.ID
		}
		items = append(items, tui.Item{Label: label, Detail: m.ID})
	}

	return items
}

// activeProfileIndex — the active profile's index from state (Enter keeps
// the current one; no state/profile — 0).
func activeProfileIndex(cfg *config.Config, providers *models.Providers) int {
	var state models.State
	if err := jsonfile.ReadJSON(cfg.StatePath(), &state); err != nil {
		return 0
	}
	for i, key := range providers.Providers.Keys {
		if key == state.Profile {
			return i
		}
	}

	return 0
}

// defaultModelIndex — the index of the profile's default model.
func defaultModelIndex(profile *models.Profile) int {
	for i, m := range profile.Models {
		if m.Default {
			return i
		}
	}

	return 0
}
