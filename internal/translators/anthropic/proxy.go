package anthropic

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// hopHeaders are hop-by-hop headers, not forwarded by the proxy (RFC 7230).
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// upstreamRequest builds the request to the upstream: the client's path/query,
// the body as is, the profile's authorization (Bearer|x-api-key) instead of the
// client's daemon token.
func (t *Translator) upstreamRequest(r *http.Request, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, t.profile.BaseURL+r.URL.RequestURI(), body) //nolint:gosec // base_url comes from the owner config
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	for key, vals := range r.Header {
		if isHopHeader(key) {
			continue
		}
		for _, v := range vals {
			req.Header.Add(key, v)
		}
	}
	// authorization: the client's daemon token is replaced with the profile's credentials
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	if t.profile.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.profile.AuthToken)
	} else if t.profile.APIKey != "" {
		req.Header.Set("X-Api-Key", t.profile.APIKey)
	}
	for key, val := range t.profile.Headers {
		req.Header.Set(key, val)
	}

	return req, nil
}

func isHopHeader(name string) bool {
	for _, h := range hopHeaders {
		if strings.EqualFold(name, h) {
			return true
		}
	}

	return false
}

// relay copies the upstream response to the client with a flush after each chunk (SSE).
func relay(w http.ResponseWriter, resp *http.Response) {
	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *Translator) proxy(w http.ResponseWriter, r *http.Request) {
	req, err := t.upstreamRequest(r, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())

		return
	}
	resp, err := t.http.Do(req) //nolint:gosec // base_url from the owner config
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())

		return
	}
	defer func() { _ = resp.Body.Close() }()
	relay(w, resp)
}

// ServeMessages: POST /v1/messages (forward with body and SSE as is).
func (t *Translator) ServeMessages(w http.ResponseWriter, r *http.Request) {
	t.proxy(w, r)
}

// ServeCountTokens: POST /v1/messages/count_tokens (forward).
func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	t.proxy(w, r)
}

// writeError writes errors in the Anthropic format.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // msg is escaped by %q //nolint:gosec // msg is escaped by %q
}
