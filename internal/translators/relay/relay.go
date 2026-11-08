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
	"strconv"
	"strings"
	"unicode/utf8"
)

// WriteErr answers with the Anthropic error envelope (api_error).
func WriteErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // %q escapes
}

// UpstreamError relays an upstream failure: a JSON body passes through as is
// (it is already the provider's error document), anything else is wrapped
// into the Anthropic envelope with the status code and a bounded excerpt of
// the body — the provider's plain-text/HTML reason ("rate limited",
// "Forbidden to use automatization") is the user's only diagnostic, and
// "upstream 429" alone hides it. The body is read up to 64 KiB and the
// excerpt capped well below that — a larger body is a runaway, not a
// message.
func UpstreamError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if json.Valid(body) {
		_, _ = w.Write(body) // the error is already JSON — pass it through as is

		return
	}
	msg := "upstream " + strconv.Itoa(resp.StatusCode)
	if ex := excerpt(body); ex != "" {
		msg += ": " + ex
	}
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg)
}

// snippetCap bounds the excerpt of a non-JSON error body (bytes; the cut
// retreats to a rune boundary so no mojibake ships to the client).
const snippetCap = 512

func excerpt(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "" // an empty body stays "upstream N:" — nothing to quote
	}
	if len(text) <= snippetCap {
		return text
	}
	cut := snippetCap
	for cut > 0 && !utf8.RuneStart(text[cut]) { // don't split a multi-byte rune
		cut--
	}

	return text[:cut] + "…"
}
