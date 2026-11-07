// Package relay — the translators' shared error responses: the Anthropic
// error envelope for our own failures and the upstream-error relay. One
// implementation instead of the three drifting per-translator copies
// (codex's already diverged with a reqdump call).
package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// WriteErr answers with the Anthropic error envelope (api_error).
func WriteErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // %q escapes
}

// UpstreamError relays an upstream failure: a JSON body passes through as is
// (it is already the provider's error document), anything else is wrapped
// into the Anthropic envelope with the status code. The body is limited to
// 64 KiB — an error body larger than that is a runaway, not a message.
func UpstreamError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if json.Valid(body) {
		_, _ = w.Write(body) // the error is already JSON — pass it through as is

		return
	}
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"upstream %d"}}`, resp.StatusCode)
}
