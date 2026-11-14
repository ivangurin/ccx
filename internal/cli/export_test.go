package cli

import (
	"context"

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

// SetCodexLoginForTest swaps the codex login seam (tests only); returns the
// restore func — tests must wire it via t.Cleanup.
func SetCodexLoginForTest(f func(ctx context.Context, authPath string, openBrowser func(string) error) error) (restore func()) {
	orig := codexLogin
	codexLogin = f

	return func() { codexLogin = orig }
}

// SetCopilotAuthClientForTest swaps the auth client factory (tests only): the
// fake is returned for every store; storeArg records the store path the
// production code passed (assert it after the run). Returns the restore func —
// tests must wire it via t.Cleanup.
func SetCopilotAuthClientForTest(fake copilotAuthClient) (restore func(), storeArg *string) {
	got := new(string)
	orig := newCopilotAuthClient
	newCopilotAuthClient = func(store string) copilotAuthClient {
		*got = store

		return fake
	}
	restore = func() { newCopilotAuthClient = orig }

	return restore, got
}

// LaunchOpenerForTest re-exports the opener launcher (tests only).
var LaunchOpenerForTest = launchOpener
