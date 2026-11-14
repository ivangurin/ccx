package tester

import (
	"strings"

	"github.com/ivangurin/ccx/internal/models"
)

// probeID adapts the configured model id to what the upstream accepts:
// a trailing "[…]" bracket suffix (Claude Code's [1m] 1M-context marker)
// is a CLIENT-side convention — z.ai rejects "glm-5.3[1m]" with 1211
// Unknown Model (verified live 2026-11-09: the same id passed earlier that
// day, the catalog lists only bare ids). Only anthropic profiles are
// stripped: other protocols never carried bracket ids.
func probeID(protocol models.Protocol, id string) string {
	if protocol != models.ProtocolAnthropic {
		return id
	}
	if i := strings.IndexByte(id, '['); i > 0 {
		return id[:i]
	}

	return id
}
