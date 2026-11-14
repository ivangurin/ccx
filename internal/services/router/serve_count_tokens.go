package router

import (
	"bytes"
	"io"
	"net/http"

	"github.com/ivangurin/ccx/internal/pkg/reqdump"
)

// ServeCountTokens delegates /v1/messages/count_tokens (same dump treatment:
// its payload mirrors /v1/messages).
func (s *Service) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	if reqdump.Enabled() {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeReadErr(w)

			return
		}
		reqdump.Write("anthropic-count-tokens", body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	s.translator.ServeCountTokens(w, r)
}
