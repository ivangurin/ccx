package provider

import (
	"github.com/ivangurin/ccx/internal/services/switcher"
)

// clients — lazily-built infrastructure clients.
type clients struct {
	daemon switcher.Daemon
}

// GetDaemon — the daemon client (lazy singleton): admin-stop, detached
// spawn and health checks over this home's daemon.pid / daemon.lock.
func (p *Provider) GetDaemon() switcher.Daemon {
	if p.clients.daemon == nil {
		p.clients.daemon = switcher.NewHTTPDaemon(p.cfg)
	}

	return p.clients.daemon
}
