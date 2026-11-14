// Package provider — the CLI process's service provider: lazy singletons
// built over one config, grouped per concern (clients.go, services.go). The
// shape mirrors idp-kube's internal/service_provider; the scope is
// deliberately narrower: ccx's daemon-process singletons (the router route,
// translators, the OAuth clients behind them) are keyed by the --provider
// launch parameter and stay inside router.Build (Ready-before-listening is
// the daemon's startup invariant, not a getter).
package provider

import (
	"github.com/ivangurin/ccx/internal/config"
)

// Provider — lazy singletons over one resolved config.
type Provider struct {
	cfg      *config.Config
	clients  clients
	services services
}

// NewEnv builds the provider over the environment's config (CCX_HOME,
// CCX_SETTINGS, CCX_PORT — the config.NewFromEnv resolution).
func NewEnv() (*Provider, error) {
	cfg, err := config.NewFromEnv()
	if err != nil {
		return nil, err
	}

	return New(cfg), nil
}

// New builds a provider over an explicit config (tests pass the sandbox one).
func New(cfg *config.Config) *Provider {
	return &Provider{cfg: cfg}
}

// GetConfig — the provider's config (resolved at NewEnv, no laziness).
func (p *Provider) GetConfig() *config.Config {
	return p.cfg
}
