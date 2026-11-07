package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

func setStatusState(t *testing.T, cfg *config.Config, profile string) {
	t.Helper()
	require.NoError(t, jsonfile.WriteAtomicJSON(cfg.StatePath(), models.State{
		Profile: profile, Model: "claude-sonnet-4.6", Updated: "2026-11-07T12:00:00Z",
	}, 0o600))
}

// The quota panel shows up exactly when the ACTIVE provider speaks copilot —
// the pool is meaningless for every other protocol.
func TestStatus_CopilotQuotaShownForCopilotProvider(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("work", &models.Profile{Name: "Work Copilot", Protocol: models.ProtocolCopilot})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	setStatusState(t, sp.Config, "work")

	old := copilotQuota
	copilotQuota = func(string) (*copilot.QuotaInfo, error) {
		return &copilot.QuotaInfo{
			CopilotPlan: "INDIVIDUAL",
			PremiumInteractions: &copilot.PremiumQuota{
				Entitlement: 300, Remaining: 37, ResetDate: "2026-12-01",
			},
		}, nil
	}
	t.Cleanup(func() { copilotQuota = old })

	var out bytes.Buffer
	require.Equal(t, 0, runStatus(nil, &out))

	require.Contains(t, out.String(), "Copilot quota")
	require.Contains(t, out.String(), "INDIVIDUAL")
	require.Contains(t, out.String(), "37/300")
	require.Contains(t, out.String(), "2026-12-01")
}

func TestStatus_NoQuotaForOtherProtocols(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("relay", &models.Profile{Name: "Relay", Protocol: models.ProtocolOpenAI})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	setStatusState(t, sp.Config, "relay")

	var out bytes.Buffer
	require.Equal(t, 0, runStatus(nil, &out))

	require.NotContains(t, out.String(), "Copilot quota")
}

// A quota fetch failure must not fail the whole status — one warning line.
func TestStatus_QuotaFailureIsAWarning(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("work", &models.Profile{Name: "Work Copilot", Protocol: models.ProtocolCopilot})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	setStatusState(t, sp.Config, "work")

	old := copilotQuota
	copilotQuota = func(string) (*copilot.QuotaInfo, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { copilotQuota = old })

	var out bytes.Buffer
	require.Equal(t, 0, runStatus(nil, &out))

	require.Contains(t, out.String(), "Copilot quota:  unavailable")
}

// No pool reported (business accounts) — the panel says so instead of
// inventing zeros.
func TestStatus_QuotaWithoutPool(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("work", &models.Profile{Name: "Work Copilot", Protocol: models.ProtocolCopilot})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	setStatusState(t, sp.Config, "work")

	old := copilotQuota
	copilotQuota = func(string) (*copilot.QuotaInfo, error) {
		return &copilot.QuotaInfo{CopilotPlan: "BUSINESS"}, nil
	}
	t.Cleanup(func() { copilotQuota = old })

	var out bytes.Buffer
	require.Equal(t, 0, runStatus(nil, &out))

	require.Contains(t, out.String(), "BUSINESS")
	require.Contains(t, out.String(), "no premium pool reported")
}
