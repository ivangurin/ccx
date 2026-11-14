// Package router — the daemon's single route: the provider named at launch
// (--provider) is resolved from providers.json once at startup; the built
// translator serves for the daemon's whole life (switching = daemon restart
// with new launch params, owned by the CLI switcher).
//
// One operation per file: router.go (the Service type + Build + the
// protocol→translator registry), active.go, serve_messages.go,
// serve_count_tokens.go, models.go, close.go.
package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ivangurin/ccx/internal/clients/codex"
	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
	codex_tr "github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/ivangurin/ccx/internal/translators/copilot"
	"github.com/ivangurin/ccx/internal/translators/openai"
)

// Service — the daemon's fixed route. Built once by Build before listening;
// immutable afterwards (the daemon serves exactly one provider per process).
type Service struct {
	profile       string
	protocol      models.Protocol
	translator    translators.Translator
	providersPath string // Models() rereads providers.json on demand
}

// Build loads providers.json, resolves the profile by key, builds the
// translator and runs its readiness check. Any failure must abort the daemon
// startup BEFORE listening — /health answering then means "route ready".
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger, providerKey string) (*Service, error) {
	providers, err := config.LoadProviders(cfg.ProvidersPath())
	if err != nil {
		return nil, fmt.Errorf("router: providers.json: %w", err)
	}

	profile := providers.Providers.Get(providerKey)
	if profile == nil {
		return nil, fmt.Errorf("router: unknown provider: %s", providerKey)
	}
	// Credential fields may hold env references ($NAME / ${NAME}): expand
	// them here — the daemon serves the expanded secret for its whole life.
	// A missing variable aborts BEFORE listening, keeping the /health ==
	// route-ready invariant honest. The providers object stays in memory
	// only; the reference in providers.json is never rewritten.
	if err := config.ExpandCredentials(profile); err != nil {
		return nil, fmt.Errorf("router: provider %s: %w", providerKey, err)
	}

	tr, err := buildTranslator(cfg, providerKey, profile)
	if err != nil {
		return nil, err
	}
	if err := tr.Ready(ctx); err != nil {
		_ = tr.Close(ctx)

		return nil, fmt.Errorf("router: provider %s not ready: %w", providerKey, err)
	}
	log.Info("route built", "provider", providerKey, "protocol", profile.Protocol)

	return &Service{
		profile:       providerKey,
		protocol:      profile.Protocol,
		translator:    tr,
		providersPath: cfg.ProvidersPath(),
	}, nil
}

// buildTranslator — the protocol → translator registry. OAuth protocols
// (copilot, codex) draw their credentials from the provider's own store:
// <CCX_HOME>/auth/<key>.json (written by ccx auth <key>).
func buildTranslator(cfg *config.Config, providerKey string, profile *models.Profile) (translators.Translator, error) {
	switch profile.Protocol {
	case models.ProtocolAnthropic:
		if profile.BaseURL == "" {
			return nil, errors.New("profile has no base_url — fill it in via ccx edit")
		}

		return anthropic.New(profile), nil
	case models.ProtocolOpenAI:
		if profile.BaseURL == "" {
			return nil, errors.New("profile has no base_url — fill it in via ccx edit")
		}

		return openai.New(profile), nil
	case models.ProtocolCopilot:
		return copilot.New(profile, copilot_client.New(cfg.AuthStorePath(providerKey))), nil
	case models.ProtocolCodex:
		return codex_tr.New(profile, codex.New(cfg.AuthStorePath(providerKey))), nil
	default:
		return nil, fmt.Errorf("router: unknown protocol %q", profile.Protocol)
	}
}
