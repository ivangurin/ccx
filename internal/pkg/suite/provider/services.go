package suite_provider

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/ivangurin/ccx/internal/services/switcher"
)

// services — the lazy production services built over the client seams.
type services struct {
	switcher *switcher.Switcher
	routes   map[string]*router.Service
}

// GetSwitcher — the real profile switcher over GetDaemon() (the mock by
// default): tests wire the daemon expectations and drive the production
// switcher — the idp-kube GetHelmService-over-GetHelmClient pattern.
func (p *Provider) GetSwitcher() *switcher.Switcher {
	if p.services.switcher == nil {
		p.services.switcher = switcher.New(p.Config, p.GetDaemon())
	}

	return p.services.switcher
}

// GetRoute — the daemon-side route built over the sandbox (router.Build
// with Ready-before-listening inside), cached per key and closed by the
// Cleaner. Deliberately NOT the production single slot: tests legitimately
// flip between providers, so keys cache separately. Panics on a build
// failure — a route that cannot build is a fixture bug, the same contract
// the reference suite's redis/repo getters have.
func (p *Provider) GetRoute(key string) *router.Service {
	if svc, ok := p.services.routes[key]; ok {
		return svc
	}
	svc, err := router.Build(p.Context(), p.Config, p.GetLogger(), key)
	if err != nil {
		panic(fmt.Sprintf("suite provider: route %q: %v", key, err))
	}
	if p.services.routes == nil {
		p.services.routes = map[string]*router.Service{}
	}
	p.services.routes[key] = svc
	p.cleanups = append(p.cleanups, func() { _ = svc.Close(context.Background()) })

	return svc
}
