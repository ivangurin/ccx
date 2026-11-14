// Package provider — the service provider: lazy singletons built over one
// config, grouped per concern (clients.go, services.go). The shape mirrors
// idp-kube's internal/service_provider. Consumers: the CLI commands (use /
// restore / stop via NewEnv) and the daemon runtime — app.Run is built over
// the provider too (GetConfig / GetLogger / GetRoute), which is why the
// daemon launch primitives live in the leaf internal/pkg/spawn (otherwise
// app → provider → switcher → app would cycle).
package provider

import (
	"log/slog"

	"github.com/ivangurin/ccx/internal/config"
)

// Provider — lazy singletons over one resolved config.
type Provider struct {
	cfg      *config.Config
	log      *slog.Logger
	clients  clients
	services services
}

// NewEnv builds the provider over the environment's config (CCX_HOME,
// CCX_SETTINGS, CCX_PORT — the config.NewFromEnv resolution). The CLI tier
// needs no logger — GetLogger falls back to a discard handler.
func NewEnv() (*Provider, error) {
	cfg, err := config.NewFromEnv()
	if err != nil {
		return nil, err
	}

	return New(cfg, nil), nil
}

// New builds a provider over an explicit config; log is the diagnostics
// logger the daemon side hands over (serve logger; nil = discard).
func New(cfg *config.Config, log *slog.Logger) *Provider {
	return &Provider{cfg: cfg, log: log}
}

// GetConfig — the provider's config (resolved at NewEnv/New, no laziness).
func (p *Provider) GetConfig() *config.Config {
	return p.cfg
}

// GetLogger — the diagnostics logger handed over at construction; a discard
// handler when the CLI tier did not bring one.
func (p *Provider) GetLogger() *slog.Logger {
	if p.log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return p.log
}
