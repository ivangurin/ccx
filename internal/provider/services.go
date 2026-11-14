package provider

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/ivangurin/ccx/internal/services/switcher"
	"github.com/ivangurin/ccx/internal/services/tester"
)

// services — the lazily-built application services over the clients.
type services struct {
	switcher *switcher.Service
	route    *router.Service
	routeKey string
	tester   *tester.Service
}

// GetSwitcher — the profile switcher (lazy singleton), built over GetDaemon().
func (p *Provider) GetSwitcher() *switcher.Service {
	if p.services.switcher == nil {
		p.services.switcher = switcher.NewService(p.cfg, p.GetDaemon())
	}

	return p.services.switcher
}

// GetRoute — the daemon's single route (lazy singleton): router.Build loads
// providers.json, resolves the profile, builds the translator and runs its
// readiness check — Ready-before-listening lives here, so /health answering
// means the route is ready. providerKey pins the slot: the daemon serves
// exactly one provider per process, a second key on the same provider is a
// caller bug and errors out.
func (p *Provider) GetRoute(ctx context.Context, providerKey string) (*router.Service, error) {
	if p.services.route != nil {
		if p.services.routeKey != providerKey {
			return nil, fmt.Errorf("provider: route already built for %q — one provider per daemon process", p.services.routeKey)
		}

		return p.services.route, nil
	}
	svc, err := router.NewService(ctx, p.cfg, p.GetLogger(), providerKey)
	if err != nil {
		return nil, err
	}
	p.services.route, p.services.routeKey = svc, providerKey

	return svc, nil
}

// GetTester — the ccx test engine (lazy singleton) over the provider's
// config; run parameters (the --provider filter, Version, the live-view
// callback) stay per-call in Options.
func (p *Provider) GetTester() *tester.Service {
	if p.services.tester == nil {
		p.services.tester = tester.NewService(p.cfg)
	}

	return p.services.tester
}
