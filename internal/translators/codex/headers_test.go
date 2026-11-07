package codex_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/stretchr/testify/require"
)

// captureUpstream records the upstream request's headers and body.
type captured struct {
	mu      sync.Mutex
	headers http.Header
	body    []byte
}

func (c *captured) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.headers, c.body = r.Header.Clone(), body
		c.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
	})
}

func (c *captured) header(k string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.headers.Get(k)
}

func (c *captured) bodyJSON(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var m map[string]any
	require.NoError(t, json.Unmarshal(c.body, &m))

	return m
}

func streamRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("X-Claude-Code-Session-Id", "sess-aaaaaaaaaaaaaaaaaaaa")

	return req
}

// The backend gates model availability by the client version — the version
// header and a matching UA must ride every request.
func TestCodex_VersionHeader(t *testing.T) {
	capt := &captured{}
	up := httptest.NewServer(capt.handler())
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	rec := httptest.NewRecorder()
	tr.ServeMessages(rec, streamRequest(`{"model":"gpt-5","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))

	require.Equal(t, "0.159.0", capt.header("Version"))
	require.Equal(t, "codex_cli_rs", capt.header("Originator"))
	require.Equal(t, "codex_cli_rs/0.159.0", capt.header("User-Agent"))
}

// A resolvable session id rides upstream as the codex-client session headers
// and the prompt cache key — same conversation, same cache.
func TestCodex_SessionHeadersAndCacheKey(t *testing.T) {
	capt := &captured{}
	up := httptest.NewServer(capt.handler())
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	rec := httptest.NewRecorder()
	tr.ServeMessages(rec, streamRequest(`{"model":"gpt-5","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))

	require.Equal(t, "sess-aaaaaaaaaaaaaaaaaaaa", capt.header("Session-Id"))
	require.Equal(t, "sess-aaaaaaaaaaaaaaaaaaaa", capt.header("X-Client-Request-Id"))
	require.Equal(t, "sess-aaaaaaaaaaaaaaaaaaaa:0", capt.header("X-Codex-Window-Id"))
	require.Equal(t, "sess-aaaaaaaaaaaaaaaaaaaa", capt.bodyJSON(t)["prompt_cache_key"])
}

// No session resolvable → none of the session artifacts appear (an empty
// cache key or a blank session header is worse than none).
func TestCodex_NoSessionNoHeaders(t *testing.T) {
	capt := &captured{}
	up := httptest.NewServer(capt.handler())
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-5","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	tr.ServeMessages(rec, req)

	require.Empty(t, capt.header("Session-Id"))
	require.Empty(t, capt.header("X-Client-Request-Id"))
	require.Empty(t, capt.header("X-Codex-Window-Id"))
	require.NotContains(t, capt.bodyJSON(t), "prompt_cache_key")
}
