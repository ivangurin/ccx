package router

import (
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

// Models — /v1/models from the launched profile's models.
func (s *Service) Models() []models.Model {
	providers, err := config.LoadProviders(s.providersPath)
	if err != nil {
		return nil
	}
	if p := providers.Providers.Get(s.profile); p != nil {
		return p.Models
	}

	return nil
}
