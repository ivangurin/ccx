package cli

import (
	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/tui"
)

// MenuStagesForTest exposes the menu stage-chain builder (tests only).
func MenuStagesForTest(providers *models.Providers, defProfile int) tui.NextStage {
	return menuStages(providers, defProfile)
}

// SetQuotaFetcherForTest swaps the copilot quota fetcher seam (tests only);
// returns the restore func — tests must wire it via t.Cleanup.
func SetQuotaFetcherForTest(f func(storePath string) (*copilot_client.QuotaInfo, error)) (restore func()) {
	orig := copilotQuota
	copilotQuota = f

	return func() { copilotQuota = orig }
}
