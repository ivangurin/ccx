package suite_provider

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/services/router"
	"github.com/ivangurin/ccx/internal/services/switcher"
	"github.com/ivangurin/ccx/internal/services/tester"
)

// services — the lazy production services built over the client seams.
type services struct {
	switcher *switcher.Service
	routes   map[string]*router.Service
	tester   *tester.Service
}

// GetSwitcherService — the real profile switcher over GetDaemon() (the mock by
// default): tests wire the daemon expectations and drive the production
// switcher — the reference Service-over-mocked-getter pattern.
func (p *Provider) GetSwitcherService() *switcher.Service {
	if p.services.switcher == nil {
		p.services.switcher = switcher.NewService(p.Config, p.GetDaemon())
	}

	return p.services.switcher
}

// GetRouteService — the daemon-side route built over the sandbox (router.NewService
// with Ready-before-listening inside), cached per key and closed by the
// Cleaner. Deliberately NOT the production single slot: tests legitimately
// flip between providers, so keys cache separately. Panics on a build
// failure — a route that cannot build is a fixture bug, the same contract
// the reference suite's redis/repo getters have.
func (p *Provider) GetRouteService(key string) *router.Service {
	if svc, ok := p.services.routes[key]; ok {
		return svc
	}
	svc, err := router.NewService(p.Context(), p.Config, p.GetLogger(), key)
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

// GetTesterService — the real ccx test engine over the sandbox config (lazy
// singleton); run parameters stay per-call in Options.
func (p *Provider) GetTesterService() *tester.Service {
	if p.services.tester == nil {
		p.services.tester = tester.NewService(p.Config)
	}

	return p.services.tester
}
