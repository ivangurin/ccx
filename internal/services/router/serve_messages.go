package router

import (
	"bytes"
	"io"
	"net/http"

	"github.com/ivangurin/ccx/internal/pkg/reqdump"
)

// ServeMessages delegates /v1/messages to the route's translator. The raw
// client body is peeked for a dump when --debug enabled dumping (see
// internal/pkg/reqdump): the exact payload a real client sends is the only
// reproduction for some upstream bugs.
func (s *Service) ServeMessages(w http.ResponseWriter, r *http.Request) {
	if reqdump.Enabled() {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeReadErr(w)

			return
		}
		reqdump.Write("anthropic-req", body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	s.translator.ServeMessages(w, r)
}
