package cli

import (
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// runStatus — ccx status: активный профиль/модель из ccx-state.json
// (демон и drift-проверка добавятся в M2, когда появится сам демон).
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
		fmt.Fprintln(stdout, "Профиль не активен — выберите: ccx <профиль> [модель]")

		return 0
	}

	if prof := providers.Profiles.Get(state.Profile); prof == nil {
		fmt.Fprintf(stdout, "Активный профиль: %s (нет в providers.json — удалён?)\n", state.Profile)
	} else {
		fmt.Fprintf(stdout, "Активный профиль: %s (%s) · %s\n", state.Profile, prof.Name, prof.Protocol)
	}
	fmt.Fprintf(stdout, "Модель:           %s\n", state.Model)
	fmt.Fprintf(stdout, "Обновлён:         %s\n", state.Updated)

	return 0
}
