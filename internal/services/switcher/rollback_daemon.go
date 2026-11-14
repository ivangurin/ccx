package switcher

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// rollbackDaemon respawns the daemon for the provider named by state.json.
// A previous direct-anthropic provider needs no daemon (settings.json still
// points straight at its upstream); no recorded state (the very first
// switch) means nothing was serving either.
func (s *Service) rollbackDaemon(ctx context.Context, providers *models.Providers, port int, debug bool) error {
	var state models.State
	if err := jsonfile.ReadJSON(s.cfg.StatePath(), &state); err != nil {
		// an unreadable/absent state file means no previous provider is on
		// record (the very first switch) — nothing was serving, nothing to
		// restore; this is not a rollback failure
		return nil //nolint:nilerr // deliberate: "nothing to roll back" is success here
	}
	if state.Provider == "" {
		return nil
	}
	prev := providers.Providers.Get(state.Provider)
	if prev == nil {
		return fmt.Errorf("previous provider %q is gone from providers.json — pick one: ccx <provider>", state.Provider)
	}
	if prev.Protocol == models.ProtocolAnthropic {
		return nil
	}
	if err := s.daemon.Restart(ctx, state.Provider, port, debug); err != nil {
		return fmt.Errorf("%s did not come back up (%v) — run: ccx %s", prev.Name, err, state.Provider)
	}

	return nil
}
