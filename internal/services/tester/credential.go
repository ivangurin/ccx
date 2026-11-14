package tester

import (
	"github.com/ivangurin/ccx/internal/models"
)

// credential — the client credential the daemon expects (auth_token, or the
// "ccx" placeholder when the profile carries none).
func credential(p *models.Profile) string {
	if p.AuthToken != "" {
		return p.AuthToken
	}

	return "ccx"
}
