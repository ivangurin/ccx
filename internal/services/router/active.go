package router

import (
	"github.com/ivangurin/ccx/internal/models"
)

// Active — (provider key, protocol, whether a route exists; always true for a
// Service returned by Build).
func (s *Service) Active() (provider string, protocol models.Protocol, ok bool) {
	if s == nil || s.translator == nil {
		return "", "", false
	}

	return s.profile, s.protocol, true
}
