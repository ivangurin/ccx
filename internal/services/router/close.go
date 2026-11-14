package router

import (
	"context"
)

// Close quenches the translator.
func (s *Service) Close(ctx context.Context) error {
	return s.translator.Close(ctx)
}
