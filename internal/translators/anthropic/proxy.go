package anthropic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ivangurin/ccx/internal/pkg/readguard"
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
func (t *Translator) upstreamRequest(ctx context.Context, r *http.Request, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, r.Method, t.profile.BaseURL+r.URL.RequestURI(), body) //nolint:gosec // base_url comes from the owner config
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

// maxProxyBody bounds the buffered request body (same bound as the copilot
// translator): buffering enables the one rectifying retry on 400s.
const maxProxyBody = 64 * 1024 * 1024

func (t *Translator) proxy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())

		return
	}
	// read deadlines: a hung gateway (nothing ever sent) would otherwise
	// leave the client request open forever — see readguard
	gctx, guard := readguard.Arm(r.Context(), readguard.FirstByte, readguard.Idle)
	defer guard.Stop()
	resp, err := t.upstream(gctx, r, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, guard.Err(err).Error())

		return
	}
	if resp.StatusCode == http.StatusBadRequest {
		resp = t.rectifyRetry(gctx, r, body, resp)
	}
	defer func() { _ = resp.Body.Close() }()
	resp.Body = guard.Body(resp.Body)
	relay(w, resp)
}

// upstream performs one upstream roundtrip with the buffered body.
func (t *Translator) upstream(ctx context.Context, r *http.Request, body []byte) (*http.Response, error) {
	req, err := t.upstreamRequest(ctx, r, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	return t.http.Do(req) //nolint:gosec // base_url from the owner config
}

// rectifyRetry consumes a 400 response, and when the error text names a
// fixable thinking-contract violation, retries ONCE with the fixed body.
// The original response's body is re-armed, so the caller can relay either
// outcome unchanged.
func (t *Translator) rectifyRetry(ctx context.Context, r *http.Request, body []byte, resp *http.Response) *http.Response {
	errText, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(errText))

	fixed, ok := rectifyBody(body, string(errText))
	if !ok {
		return resp
	}
	_ = resp.Body.Close()
	retried, err := t.upstream(ctx, r, fixed)
	if err != nil {
		// the retry failing to even connect is a plain 502 — the original
		// upstream answered, so this error is the user's best signal
		errResp := &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"rectified retry failed"}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}

		return errResp
	}

	return retried
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
