// Package translators: translators from the Anthropic Messages API to provider
// protocols. The contract is http-handler style: a translator receives the
// original request and fully owns the response (passthrough copies the stream
// byte-for-byte, protocol translators decode the body and convert SSE).
package translators

import (
	"context"
	"net/http"

	"github.com/ivangurin/ccx/internal/models"
)

// Translator is the daemon's active translator. Built by the factory on profile
// switch, lives until the next switch; in-flight requests keep their own
// reference (switching does not cut them off).
type Translator interface {
	// Protocol is the profile's protocol.
	Protocol() models.Protocol
	// Ready is the readiness check performed BEFORE switching to this
	// translator (OAuth token validity etc.); anthropic/openai — trivially OK.
	Ready(ctx context.Context) error
	// ServeMessages handles POST /v1/messages (stream and non-stream).
	ServeMessages(w http.ResponseWriter, r *http.Request)
	// ServeCountTokens handles POST /v1/messages/count_tokens.
	ServeCountTokens(w http.ResponseWriter, r *http.Request)
	// Close releases resources after drain.
	Close(ctx context.Context) error
}
