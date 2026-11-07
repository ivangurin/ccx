package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
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
// translator): buffering enables the one rectifying retry on 400s. A var so
// tests can shrink it; a body over the bound is refused loudly — a silent
// truncation would ship broken JSON upstream and surface as a baffling
// provider 400.
var maxProxyBody int64 = 64 * 1024 * 1024

// SetMaxProxyBodyForTest shrinks the body bound (tests restore nothing —
// each test sets its own).
func SetMaxProxyBodyForTest(n int64) { maxProxyBody = n }

func (t *Translator) proxy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())

		return
	}
	if int64(len(body)) > maxProxyBody {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds the passthrough bound")

		return
	}
	// usage record: model/stream from the buffered body; token counts stay
	// zero — passthrough never parses SSE by design, and sniffing every
	// relayed byte for counts was rejected (byte-precise is the point)
	entry := usagelog.Entry{
		Ts:       time.Now(),
		Provider: t.profile.Name, Protocol: string(t.Protocol()),
		Model: bodyModel(body), Stream: bodyStream(body),
		Status: http.StatusOK,
	}
	started := time.Now()
	defer func() {
		entry.LatencyMS = time.Since(started).Milliseconds()
		usagelog.Record(entry)
	}()
	// read deadlines: a hung gateway (nothing ever sent) would otherwise
	// leave the client request open forever — see readguard
	gctx, guard := readguard.Arm(r.Context(), readguard.FirstByte, readguard.Idle)
	defer guard.Stop()
	resp, err := t.upstream(gctx, r, body)
	if err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, guard.Err(err).Error()
		writeError(w, http.StatusBadGateway, entry.Error)

		return
	}
	if resp.StatusCode == http.StatusBadRequest {
		resp = t.rectifyRetry(gctx, r, body, resp)
	}
	defer func() { _ = resp.Body.Close() }()
	resp.Body = guard.Body(resp.Body)
	entry.Status = resp.StatusCode
	relay(w, resp)
}

// bodyModel/bodyStream read the two identity fields without a full decode.
func bodyModel(body []byte) string {
	var probe struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &probe) == nil {
		return probe.Model
	}

	return ""
}

func bodyStream(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	if json.Unmarshal(body, &probe) == nil {
		return probe.Stream
	}

	return false
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
