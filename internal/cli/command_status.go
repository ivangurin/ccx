package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// quotaTimeout bounds the quota fetch: ccx status is a local-state command,
// and a hung network (VPN come-back, DNS stall) must not freeze it — the
// panel is supplementary and its own 15s client deadline is far too patient
// for a status line. Var for tests.
var quotaTimeout = 3 * time.Second

// copilotQuota fetches the subscription quota panel (var for tests; the
// default talks to the API with the cached login).
var copilotQuota = func(storePath string) (*copilot_client.QuotaInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaTimeout)
	defer cancel()

	return copilot_client.New(storePath).Quota(ctx)
}

// runStatus — ccx status: active provider/model from state.json; a
// Copilot quota panel rides along when the active provider speaks copilot —
// the pool is what the daemon's quota marking spends, and status is where
// the burn becomes visible instead of felt. The local lines print BEFORE
// the panel: the quota fetch is the only network call here, and it must
// never hold the local answer hostage.
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
	if err := jsonfile.ReadJSON(cfg.StatePath(), &state); err != nil || state.Provider == "" {
		fmt.Fprintln(stdout, "No active provider — choose one: ccx <provider> [model]")

		return 0
	}

	var prof *models.Profile
	copilot := false
	if p := providers.Providers.Get(state.Provider); p == nil {
		fmt.Fprintf(stdout, "Active provider: %s (missing from providers.json — deleted?)\n", state.Provider)
	} else {
		fmt.Fprintf(stdout, "Active provider: %s (%s) · %s\n", state.Provider, p.Name, p.Protocol)
		prof = p
		copilot = prof.Protocol == models.ProtocolCopilot
	}
	fmt.Fprintf(stdout, "Model:          %s\n", state.Model)
	fmt.Fprintf(stdout, "Updated:        %s\n", state.Updated)
	printDaemonState(cfg, prof, stdout)
	if copilot {
		printCopilotQuota(cfg, state.Provider, stdout)
	}

	return 0
}

// daemonProbeTimeout bounds the liveness probe: status is a local-state
// command and must never hang on a wedged network (var for tests).
var daemonProbeTimeout = time.Second

// printDaemonState shows daemon liveness for the active route: translated
// protocols (openai/copilot/codex) route every Claude Code request through
// the daemon — a dead daemon is ECONNREFUSED in every running session, the
// exact 2026-11-09 failure mode status had no answer for ("проверял, что
// демон запущен" — nothing showed it). A direct anthropic profile uses no
// daemon by design: "down" there would be a false alarm.
func printDaemonState(cfg *config.Config, prof *models.Profile, stdout io.Writer) {
	if prof != nil && prof.Protocol == models.ProtocolAnthropic {
		fmt.Fprintln(stdout, "Daemon:         not used (direct connection)")

		return
	}
	port := config.ResolveDaemonPort(cfg, 0)
	if app.HealthCheck(fmt.Sprintf("http://127.0.0.1:%d", port), daemonProbeTimeout) {
		fmt.Fprintf(stdout, "Daemon:         up (127.0.0.1:%d)\n", port)

		return
	}
	fmt.Fprintf(stdout, "Daemon:         down (no /health answer on 127.0.0.1:%d)\n", port)
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
