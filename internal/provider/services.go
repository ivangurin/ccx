package provider

import (
	"github.com/ivangurin/ccx/internal/services/switcher"
)

// services — lazily-built application services over the clients.
type services struct {
	switcher *switcher.Switcher
}

// GetSwitcher — the profile switcher (lazy singleton), built over GetDaemon().
func (p *Provider) GetSwitcher() *switcher.Switcher {
	if p.services.switcher == nil {
		p.services.switcher = switcher.New(p.cfg, p.GetDaemon())
	}

	return p.services.switcher
}
