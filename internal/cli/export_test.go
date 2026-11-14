package cli

import (
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/tui"
)

// MenuStagesForTest exposes the menu stage-chain builder (tests only).
func MenuStagesForTest(providers *models.Providers, defProfile int) tui.NextStage {
	return menuStages(providers, defProfile)
}
