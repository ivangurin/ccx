package switcher

import (
	"fmt"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// writeState records the switch in state.json (the statusline's source).
func (s *Service) writeState(profileKey, model string) error {
	state := models.State{
		Provider: profileKey,
		Model:    model,
		Updated:  time.Now().UTC().Format(time.RFC3339),
	}

	return writeStateFile(s.cfg.StatePath(), state)
}

// writeStateFile — atomic write of state.json (format 1:1 with bash-ccx).
func writeStateFile(path string, state models.State) error {
	if err := jsonfile.WriteAtomicJSON(path, state, 0o600); err != nil {
		return fmt.Errorf("ccx: writing state: %w", err)
	}

	return nil
}
