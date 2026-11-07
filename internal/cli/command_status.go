package cli

import (
	"context"
	"fmt"
	"io"

	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// copilotQuota fetches the subscription quota panel (var for tests; the
// default talks to the API with the cached login).
var copilotQuota = func(storePath string) (*copilot_client.QuotaInfo, error) {
	return copilot_client.New(storePath).Quota(context.Background())
}

// runStatus — ccx status: active provider/model from state.json; a
// Copilot quota panel rides along when the active provider speaks copilot —
// the pool is what the daemon's quota marking spends, and status is where
// the burn becomes visible instead of felt.
func runStatus(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	providers, code := loadProvidersOrHint(cfg, stdout)
	if providers == nil {
		return code
	}

	var state models.State
	if err := jsonfile.ReadJSON(cfg.StatePath(), &state); err != nil || state.Profile == "" {
		fmt.Fprintln(stdout, "No active provider — choose one: ccx <provider> [model]")

		return 0
	}

	if prof := providers.Providers.Get(state.Profile); prof == nil {
		fmt.Fprintf(stdout, "Active provider: %s (missing from providers.json — deleted?)\n", state.Profile)
	} else {
		fmt.Fprintf(stdout, "Active provider: %s (%s) · %s\n", state.Profile, prof.Name, prof.Protocol)
		if prof.Protocol == models.ProtocolCopilot {
			printCopilotQuota(cfg, state.Profile, stdout)
		}
	}
	fmt.Fprintf(stdout, "Model:          %s\n", state.Model)
	fmt.Fprintf(stdout, "Updated:        %s\n", state.Updated)

	return 0
}

// printCopilotQuota prints the plan and the AI-credit pool from the ACTIVE
// provider's own credential store; a fetch failure is a warning line, never
// a failed status. The panel says "ai credits" — the unit the gateway
// meters since the 2026-06 usage-based billing switch (the API still names
// the field premium_interactions).
func printCopilotQuota(cfg *config.Config, providerKey string, stdout io.Writer) {
	q, err := copilotQuota(cfg.AuthStorePath(providerKey))
	if err != nil {
		fmt.Fprintf(stdout, "Copilot quota:  unavailable (%v)\n", err)

		return
	}
	fmt.Fprintf(stdout, "Copilot quota:  plan %s\n", q.CopilotPlan)
	if q.PremiumInteractions == nil {
		fmt.Fprintln(stdout, "  ai credits: no pool reported")

		return
	}
	p := q.PremiumInteractions
	if p.Unlimited {
		fmt.Fprintln(stdout, "  ai credits: unlimited")

		return
	}
	reset := p.ResetDate
	if reset == "" {
		reset = "unknown"
	}
	fmt.Fprintf(stdout, "  ai credits: %d/%d remaining · resets %s\n",
		p.Remaining, p.Entitlement, reset)
}
