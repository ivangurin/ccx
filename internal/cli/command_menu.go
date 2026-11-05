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

// runMenu — ccx без аргументов: профиль → модель → use.
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
		fmt.Fprintln(stdout, "Профилей нет — ccx init или ccx edit")

		return 0
	}

	defProfile := activeProfileIndex(cfg, providers)
	profileItems := profileMenuItems(providers)
	pIdx, err := tui.Select("Профиль", profileItems, defProfile, os.Stdin, stdout)
	if err != nil || pIdx < 0 {
		return 0
	}
	profileKey := providers.Profiles.Keys[pIdx]
	profile := providers.Profiles.Get(profileKey)

	if len(profile.Models) == 0 {
		fmt.Fprintf(stdout, "ccx: в профиле %s нет моделей — ccx edit\n", profileKey)

		return 1
	}
	defModel := defaultModelIndex(profile)
	modelItems := modelMenuItems(profile)
	mIdx, err := tui.Select("Модель · "+profileKey, modelItems, defModel, os.Stdin, stdout)
	if err != nil || mIdx < 0 {
		return 0
	}
	model := profile.Models[mIdx].ID

	fmt.Fprint(stdout, "\n")

	return runUse([]string{"use", profileKey, model}, stdout)
}

// profileMenuItems — пункты меню профилей в порядке файла.
func profileMenuItems(providers *models.Providers) []tui.Item {
	items := make([]tui.Item, 0, providers.Profiles.Len())
	for _, key := range providers.Profiles.Keys {
		items = append(items, tui.Item{Label: key, Detail: providers.Profiles.Get(key).Name})
	}

	return items
}

// modelMenuItems — пункты меню моделей профиля.
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

// activeProfileIndex — индекс активного профиля из state (Enter продолжает
// текущий; нет state/профиля — 0).
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

// defaultModelIndex — индекс default-модели профиля.
func defaultModelIndex(profile *models.Profile) int {
	for i, m := range profile.Models {
		if m.Default {
			return i
		}
	}

	return 0
}
