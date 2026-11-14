package provider

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/ivangurin/ccx/internal/services/switcher"
)

// services — the lazily-built application services over the clients.
type services struct {
	switcher *switcher.Switcher
	route    *router.Service
	routeKey string
}

// GetSwitcher — the profile switcher (lazy singleton), built over GetDaemon().
func (p *Provider) GetSwitcher() *switcher.Switcher {
	if p.services.switcher == nil {
		p.services.switcher = switcher.New(p.cfg, p.GetDaemon())
	}

	return p.services.switcher
}

// GetRoute — the daemon's single route (lazy singleton): router.Build loads
// providers.json, resolves the profile, builds the translator and runs its
// readiness check — Ready-before-listening lives here, so /health answering
// still means the route is ready. providerKey pins the slot: the daemon
// serves exactly one provider per process (switching = daemon restart), a
// second key on the same provider is a caller bug and errors out.
func (p *Provider) GetRoute(ctx context.Context, providerKey string) (*router.Service, error) {
	if p.services.route != nil {
		if p.services.routeKey != providerKey {
			return nil, fmt.Errorf("provider: route already built for %q — one provider per daemon process", p.services.routeKey)
		}

		return p.services.route, nil
	}
	svc, err := router.Build(ctx, p.cfg, p.GetLogger(), providerKey)
	if err != nil {
		return nil, err
	}
	p.services.route, p.services.routeKey = svc, providerKey

	return svc, nil
}
