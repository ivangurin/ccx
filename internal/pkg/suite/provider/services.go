package suite_provider

import (
	"github.com/ivangurin/ccx/internal/services/switcher"
)

// services — the lazy production services built over the client seams.
type services struct {
	switcher *switcher.Switcher
}

// GetSwitcher — the real profile switcher over GetDaemon() (the mock by
// default): tests wire the daemon expectations and drive the production
// switcher — the idp-kube GetHelmService-over-GetHelmClient pattern.
// The route side (router.Service, the translators, the keyed OAuth clients)
// stays outside the suite on purpose: it is keyed by the --provider launch
// parameter (see internal/provider for the boundary rationale).
func (p *Provider) GetSwitcher() *switcher.Switcher {
	if p.services.switcher == nil {
		p.services.switcher = switcher.New(p.Config, p.GetDaemon())
	}

	return p.services.switcher
}
