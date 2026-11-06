// Package anthropic: passthrough translator for a direct Anthropic-compatible
// upstream. Body and SSE are copied byte-for-byte (zero risk of transformation),
// only the upstream authorization is replaced with the profile's.
package anthropic

import (
	"context"
	"net/http"

	"github.com/ivangurin/ccms/internal/models"
)

// Translator is a passthrough to profile.BaseURL.
type Translator struct {
	profile *models.Profile
	http    *http.Client
}

// New builds a passthrough translator.
func New(profile *models.Profile) *Translator {
	return &Translator{
		profile: profile,
		http:    &http.Client{}, // no timeouts: SSE lives long
	}
}

// Protocol is anthropic.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolAnthropic
}

// Ready is trivially ready (native protocol, no OAuth needed).
func (t *Translator) Ready(_ context.Context) error {
	return nil
}

// Close: no resources.
func (t *Translator) Close(_ context.Context) error {
	return nil
}
