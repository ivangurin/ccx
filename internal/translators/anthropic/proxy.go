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

// SetMaxProxyBodyForTest shrinks the body bound and returns the restore —
// a bare mutation would leak into every later test in the package.
func SetMaxProxyBodyForTest(n int64) (restore func()) {
	old := maxProxyBody
	maxProxyBody = n

	return func() { maxProxyBody = old }
}

func (t *Translator) proxy(w http.ResponseWriter, r *http.Request, recordUsage bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())

		return
	}
	if int64(len(body)) > maxProxyBody {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds the passthrough bound")

		return
	}
	// one shallow probe for both identity fields — the body can be tens of
	// megabytes, parsing it twice for two scalars is waste on the hot path
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)

	// usage record: model/stream from the buffered body; token counts stay
	// zero — passthrough never parses SSE by design, and sniffing every
	// relayed byte for counts was rejected (byte-precise is the point);
	// count_tokens is not a billable interaction and records nothing
	entry := usagelog.Entry{
		Ts:       time.Now(),
		Provider: t.profile.Name, Protocol: string(t.Protocol()),
		Model: probe.Model, Stream: probe.Stream,
		Status: http.StatusOK,
	}
	started := time.Now()
	if recordUsage {
		defer func() {
			entry.LatencyMS = time.Since(started).Milliseconds()
			usagelog.Record(entry)
		}()
	}
	// read deadlines: a hung gateway (nothing ever sent) would otherwise
	// leave the client request open forever — see readguard; a non-stream
	// answer's first byte IS the finished generation, so it gets the
	// whole-generation budget, not the first-token patience
	gctx, guard := readguard.Arm(r.Context(), readguard.FirstByteFor(probe.Stream), readguard.Idle)
	defer guard.Stop()
	resp, err := t.upstream(gctx, r, body)
	if err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, guard.Err(err).Error()
		writeError(w, http.StatusBadGateway, entry.Error)

		return
	}
	if resp.StatusCode == http.StatusBadRequest {
		resp = t.rectifyRetry(r, body, probe.Stream, resp)
	}
	defer func() { _ = resp.Body.Close() }()
	resp.Body = guard.Body(resp.Body)
	entry.Status = resp.StatusCode
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
// fixable thinking-contract violation, retries ONCE with the fixed body —
// under a FRESH guard: the first attempt already spent most of the armed
// budget, and a fixable request must not die on the stale remainder. The
// original response's body is re-armed, so the caller can relay either
// outcome unchanged.
func (t *Translator) rectifyRetry(r *http.Request, body []byte, stream bool, resp *http.Response) *http.Response {
	errText, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(errText))

	fixed, ok := rectifyBody(body, string(errText))
	if !ok {
		return resp
	}
	_ = resp.Body.Close()
	gctx, guard := readguard.Arm(r.Context(), readguard.FirstByteFor(stream), readguard.Idle)
	defer guard.Stop()
	retried, err := t.upstream(gctx, r, fixed)
	if err != nil {
		// the retry failing mid-flight is a plain 502 carrying the real
		// cause — the original upstream answered, so this error is the
		// user's best signal
		msg := "rectified retry failed: " + guard.Err(err).Error()
		errResp := &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"` + msg + `"}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}

		return errResp
	}

	return retried
}

// ServeMessages: POST /v1/messages (forward with body and SSE as is).
func (t *Translator) ServeMessages(w http.ResponseWriter, r *http.Request) {
	t.proxy(w, r, true)
}

// ServeCountTokens: POST /v1/messages/count_tokens (forward; not a billable
// interaction — no usage record).
func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	t.proxy(w, r, false)
}

// writeError writes errors in the Anthropic format.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // msg is escaped by %q //nolint:gosec // msg is escaped by %q
}
