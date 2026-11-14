package suite_provider

import (
	"github.com/ivangurin/ccx/internal/config"
)

// InitConfig builds the sandbox config: the env resolution (CCX_HOME and
// CCX_SETTINGS are pinned by NewProvider before this runs) plus safe test
// defaults — short time budgets and a silent log level. Production-only
// paths (the live ~/.claude settings default) are unreachable here by the
// env pin, not by config magic.
func InitConfig() (*config.Config, error) {
	cfg, err := config.NewFromEnv()
	if err != nil {
		return nil, err
	}
	// safe test defaults: short timeouts, silent log
	cfg.LogLevel = "error"
	cfg.LogFormat = "json"

	return cfg, nil
}
