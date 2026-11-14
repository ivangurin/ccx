package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/relay"
)

func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		relay.WriteErr(w, http.StatusBadRequest, err.Error())

		return
	}
	raw, _ := json.Marshal(req) //nolint:errchkjson // RawMessage by construction
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"input_tokens":%d}`, len(raw)/4)
}
