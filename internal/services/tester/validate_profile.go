package tester

import (
	"errors"
	"fmt"

	"github.com/ivangurin/ccx/internal/models"
)

// validateProfile — the structural phase (no network): protocol known,
// base_url present for URL-based protocols, a credential where the daemon
// holds none of its own (openai is pass-through — a probe without a token
// is guaranteed 401, testing it only burns a round-trip), models listed
// and sane.
func validateProfile(p *models.Profile) error {
	switch p.Protocol {
	case models.ProtocolAnthropic, models.ProtocolOpenAI:
		if p.BaseURL == "" {
			return errors.New("base_url is empty")
		}
		if p.AuthToken == "" {
			return errors.New("auth_token is empty — no credential to probe with")
		}
	case models.ProtocolCopilot, models.ProtocolCodex:
		// credentials live in the auth store — Ready() covers them
	default:
		return fmt.Errorf("unknown protocol %q", p.Protocol)
	}
	if len(p.Models) == 0 {
		return errors.New("no models listed")
	}
	seen := map[string]bool{}
	for _, m := range p.Models {
		if m.ID == "" {
			return errors.New("a model has no id")
		}
		if seen[m.ID] {
			return fmt.Errorf("duplicate model id: %s", m.ID)
		}
		seen[m.ID] = true
	}

	return nil
}
