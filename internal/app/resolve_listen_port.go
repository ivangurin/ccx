package app

import (
	"github.com/ivangurin/ccx/internal/config"
)

// resolveListenPort is the daemon-side fallback when --port is absent: env
// CCX_PORT, else the fixed default. providers.json daemon.* is deliberately
// NOT read — that block is switcher-owned launch config, and the switcher
// always passes an explicit --port; daemon.port reaches the daemon through
// the switcher's resolution (config.ResolveDaemonPort), never its own.
func resolveListenPort(c *config.Config, flagPort int) int {
	if flagPort > 0 {
		return flagPort
	}
	if c.DaemonPort > 0 {
		return c.DaemonPort
	}

	return config.DefaultDaemonPort
}

// ResolveListenPortForTest exports resolveListenPort for fallback tests.
var ResolveListenPortForTest = resolveListenPort
