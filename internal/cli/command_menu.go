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

// runMenu — ccx with no arguments: profile → model → use.
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
	if providers.Profiles.Len() == 0 {
		fmt.Fprintln(stdout, "No profiles — ccx init or ccx edit")

		return 0
	}

	defProfile := activeProfileIndex(cfg, providers)
	profileItems := profileMenuItems(providers)
	pIdx, err := tui.Select("Profile", profileItems, defProfile, os.Stdin, stdout)
	if err != nil || pIdx < 0 {
		return 0
	}
	profileKey := providers.Profiles.Keys[pIdx]
	profile := providers.Profiles.Get(profileKey)

	if len(profile.Models) == 0 {
		fmt.Fprintf(stdout, "ccx: profile %s has no models — ccx edit\n", profileKey)

		return 1
	}
	defModel := defaultModelIndex(profile)
	modelItems := modelMenuItems(profile)
	mIdx, err := tui.Select("Model · "+profileKey, modelItems, defModel, os.Stdin, stdout)
	if err != nil || mIdx < 0 {
		return 0
	}
	model := profile.Models[mIdx].ID

	fmt.Fprint(stdout, "\n")

	return runUse([]string{"use", profileKey, model}, stdout)
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
