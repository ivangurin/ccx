// Package anthropic — passthrough-транслятор: прямой Anthropic-совместимый
// апстрим. Тело и SSE копируются байт-в-байт (нулевой риск трансформации),
// подменяется только авторизация апстрима из профиля.
package anthropic

import (
	"context"
	"net/http"

	"github.com/ivangurin/ccx/internal/models"
)

// Translator — passthrough на profile.Anthropic.BaseURL.
type Translator struct {
	cfg  *models.AnthropicConfig
	http *http.Client
}

// New строит passthrough-транслятор.
func New(profile *models.Profile) *Translator {
	return &Translator{
		cfg:  profile.Anthropic,
		http: &http.Client{}, // без таймаутов: SSE живёт долго
	}
}

// Protocol — anthropic.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolAnthropic
}

// Ready — тривиально готов (нативный протокол, OAuth не нужен).
func (t *Translator) Ready(_ context.Context) error {
	return nil
}

// Close — нет ресурсов.
func (t *Translator) Close(_ context.Context) error {
	return nil
}
