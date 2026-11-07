package openai_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

func newTranslator(baseURL string) *openai.Translator {
	return openai.New(&models.Profile{
		Protocol: models.ProtocolOpenAI,
		BaseURL:  baseURL,
	})
}

func TestServeMessages_NonStream(t *testing.T) {
	var gotAuth, gotUA string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"the answer"},"finish_reason":"stop"}],"usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	t.Cleanup(up.Close)

	tr := newTranslator(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "the answer")
	require.Contains(t, rec.Body.String(), "end_turn")
	require.Equal(t, "Bearer sk-test", gotAuth)
	require.Equal(t, openai.DefaultUserAgent, gotUA)
}

func TestServeMessages_Stream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(up.Close)

	tr := newTranslator(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "event: message_start")
	require.Contains(t, out, "text_delta")
	require.Contains(t, out, "Hel")
	require.Contains(t, out, "lo")
	require.Contains(t, out, "event: message_stop")
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}

func TestServeMessages_UpstreamError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	t.Cleanup(up.Close)

	tr := newTranslator(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt","max_tokens":1,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), "rate limited")
}

func TestServeCountTokens_Estimate(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(up.Close)

	tr := newTranslator(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hello world"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeCountTokens(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "input_tokens")
}

func TestSerialize_MaxTokensField(t *testing.T) {
	chat := &models.ChatRequest{Model: "m", MaxTokens: 42}

	out, err := openai.SerializeForTest(chat, "max_completion_tokens")
	require.NoError(t, err)
	require.Contains(t, string(out), `"max_completion_tokens":42`)
	require.NotContains(t, string(out), `"max_tokens":42`)

	out2, err := openai.SerializeForTest(chat, "max_tokens")
	require.NoError(t, err)
	require.Contains(t, string(out2), `"max_tokens":42`)
}

// passThroughServer is an upstream that records the Authorization header and
// answers a minimal chat completion.
func passThroughServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	gotAuth := new(string)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(up.Close)

	return up, gotAuth
}

// The incoming Authorization: Bearer is forwarded verbatim (the profile holds
// no api_key — the daemon has no secret of its own).
func TestServeMessages_ForwardsBearerToken(t *testing.T) {
	up, gotAuth := passThroughServer(t)

	tr := openai.New(&models.Profile{Protocol: models.ProtocolOpenAI, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-upstream")
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Bearer sk-upstream", *gotAuth)
}

// A profile without user_agent must not leak Go's default UA: a corporate
// gateway answers exactly "Go-http-client/1.1" with "Forbidden to use
// automatization" — the masquerade default is opencode's CLI UA; a profile's
// user_agent still wins (TestServeMessages_NonStream).
func TestServeMessages_DefaultUserAgentWhenProfileEmpty(t *testing.T) {
	gotUA := new(string)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(up.Close)

	tr := openai.New(&models.Profile{Protocol: models.ProtocolOpenAI, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, openai.DefaultUserAgent, *gotUA)
}

// x-api-key (Claude Code sends it when settings.json has ANTHROPIC_API_KEY)
// falls back to Bearer upstream.
func TestServeMessages_ForwardsXAPIKeyAsBearer(t *testing.T) {
	up, gotAuth := passThroughServer(t)

	tr := openai.New(&models.Profile{Protocol: models.ProtocolOpenAI, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("X-Api-Key", "sk-via-key")
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Bearer sk-via-key", *gotAuth)
}

// No credential in the request → no Authorization header upstream at all
// (not "Bearer " with an empty token).
func TestServeMessages_NoTokenNoAuthorizationHeader(t *testing.T) {
	up, gotAuth := passThroughServer(t)

	tr := openai.New(&models.Profile{Protocol: models.ProtocolOpenAI, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, *gotAuth)
}
