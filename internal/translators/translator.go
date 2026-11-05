// Package translators — трансляторы Anthropic Messages API в протоколы
// провайдеров. Контракт — http-handler-стиль: транслятор получает исходный
// запрос и полностью владеет ответом (passthrough копирует стрим байт-в-байт,
// протокольные трансляторы декодируют тело и конвертят SSE).
package translators

import (
	"context"
	"net/http"

	"github.com/ivangurin/ccx/internal/models"
)

// Translator — активный транслятор демона. Строится фабрикой при переключении
// профиля, живёт до следующего переключения; in-flight запросы держат свою
// ссылку (переключение не рвёт их).
type Translator interface {
	// Protocol — протокол профиля.
	Protocol() models.Protocol
	// Ready — проверка готовности ДО переключения на этот транслятор
	// (валидность OAuth-токенов и т.п.); anthropic/openai — тривиально OK.
	Ready(ctx context.Context) error
	// ServeMessages обрабатывает POST /v1/messages (стрим и не-стрим).
	ServeMessages(w http.ResponseWriter, r *http.Request)
	// ServeCountTokens обрабатывает POST /v1/messages/count_tokens.
	ServeCountTokens(w http.ResponseWriter, r *http.Request)
	// Close освобождает ресурсы после drain.
	Close(ctx context.Context) error
}
