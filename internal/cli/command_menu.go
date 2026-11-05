package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
	"github.com/ivangurin/ccms/internal/pkg/tui"
)

// runMenu — ccms with no arguments: provider → model → use. A single staged
// TUI session: the model list replaces the provider list in place, Esc on
// the models returns to the providers, Esc on the providers exits.
func runMenu(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}
	providers := loadProvidersOrHint(cfg, stdout)
	if providers == nil {
		return 0
	}
	if providers.Profiles.Len() == 0 {
		fmt.Fprintln(stdout, "No profiles — ccms init or ccms edit")

		return 0
	}

	defProfile := activeProfileIndex(cfg, providers)
	next := menuStages(providers, defProfile)
	chosen, err := tui.SelectStaged(next, os.Stdin, stdout)
	if err != nil || len(chosen) == 0 {
		return 0
	}

	profileKey := providers.Profiles.Keys[chosen[0]]
	profile := providers.Profiles.Get(profileKey)
	if len(profile.Models) == 0 {
		// the chain stopped at the provider: its model level was never built
		fmt.Fprintf(stdout, "ccms: profile %s has no models — ccms edit\n", profileKey)

		return 1
	}
	model := profile.Models[chosen[1]].ID

	fmt.Fprint(stdout, "\n")

	return runUse([]string{"use", profileKey, model}, stdout)
}

// menuStages — the menu levels: providers, then the chosen provider's models.
// A provider without models ends the chain (ok=false) — runMenu reports it.
func menuStages(providers *models.Providers, defProfile int) tui.NextStage {
	return func(chosen []int) (tui.Stage, bool) {
		if len(chosen) > 0 {
			profileKey := providers.Profiles.Keys[chosen[0]]
			profile := providers.Profiles.Get(profileKey)
			if len(profile.Models) == 0 {
				return tui.Stage{}, false
			}

			return tui.Stage{
				Header:     "Choose a model · " + profileKey,
				Items:      modelMenuItems(profile),
				DefaultIdx: defaultModelIndex(profile),
			}, true
		}

		return tui.Stage{
			Header:     "Choose a provider",
			Items:      profileMenuItems(providers),
			DefaultIdx: defProfile,
		}, true
	}
}

// profileMenuItems — profile menu entries in file order.
func profileMenuItems(providers *models.Providers) []tui.Item {
	items := make([]tui.Item, 0, providers.Profiles.Len())
	for _, key := range providers.Profiles.Keys {
		items = append(items, tui.Item{Label: key, Detail: providers.Profiles.Get(key).Name})
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
	for i, key := range providers.Profiles.Keys {
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
