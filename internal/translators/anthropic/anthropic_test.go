package anthropic_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/translators/anthropic"
	"github.com/stretchr/testify/require"
)

// fakeUpstream is an Anthropic-compatible upstream: remembers the request, responds with a body.
func fakeUpstream(t *testing.T, status int, body string, chunks []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// upstream authorization is injected by the translator
		auth := r.Header.Get("Authorization")
		apiKey := r.Header.Get("X-Api-Key")
		if auth == "" && apiKey == "" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
		w.Header().Set("X-Upstream-Path", r.URL.Path)
		w.WriteHeader(status)
		if body != "" {
			_, _ = io.WriteString(w, body)
		}
		for _, chunk := range chunks {
			_, _ = io.WriteString(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func newTranslator(baseURL string) *anthropic.Translator {
	return anthropic.New(&models.Profile{
		Protocol:  models.ProtocolAnthropic,
		BaseURL:   baseURL,
		AuthToken: "upstream-secret",
	})
}

func TestPassthrough_ForwardsBodyAndPath(t *testing.T) {
	up := fakeUpstream(t, 200, `{"ok":true}`, nil)
	tr := newTranslator(up.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", strings.NewReader(`{"model":"glm"}`))
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	resp := rec.Result()
	require.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.JSONEq(t, `{"ok":true}`, string(body))
	require.Equal(t, "/v1/messages", resp.Header.Get("X-Upstream-Path"))
}

func TestPassthrough_UpstreamAuthFromProfile(t *testing.T) {
	var gotAuth, gotKey string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotKey = r.Header.Get("Authorization"), r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)

	tr := newTranslator(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer client-token") // the daemon token must not get through
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, "Bearer upstream-secret", gotAuth)
	require.Empty(t, gotKey)
}

func TestPassthrough_ApiKeyProfile(t *testing.T) {
	var gotKey string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)

	tr := anthropic.New(&models.Profile{
		Protocol: models.ProtocolAnthropic,
		BaseURL:  up.URL,
		APIKey:   "native-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, "native-key", gotKey)
}

func TestPassthrough_SSEStreamRelayed(t *testing.T) {
	up := fakeUpstream(t, 200, "", []string{"event: message_start\ndata: {\"a\":1}\n\n", "data: [DONE]\n\n"})
	tr := newTranslator(up.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"stream":true}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "message_start")
	require.Contains(t, out, "[DONE]")
}

func TestPassthrough_UpstreamErrorPassthrough(t *testing.T) {
	up := fakeUpstream(t, 429, `{"type":"error","error":{"type":"rate_limit_error"}}`, nil)
	tr := newTranslator(up.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, 429, rec.Code)
	require.Contains(t, rec.Body.String(), "rate_limit_error")
}

func TestPassthrough_ReadyOK(t *testing.T) {
	up := fakeUpstream(t, 200, "{}", nil)
	tr := newTranslator(up.URL)
	require.NoError(t, tr.Ready(httptest.NewRequest(http.MethodGet, "/", nil).Context()))
}

func TestPassthrough_CountTokens(t *testing.T) {
	up := fakeUpstream(t, 200, `{"input_tokens":42}`, nil)
	tr := newTranslator(up.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"glm"}`))
	rec := httptest.NewRecorder()

	tr.ServeCountTokens(rec, req)

	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "42")
}

func TestPassthrough_UpstreamDown(t *testing.T) {
	tr := newTranslator("http://127.0.0.1:1") // nobody listens
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "error")
}

func TestTranslator_TrivialLifecycle(t *testing.T) {
	tr := newTranslator("http://upstream.invalid")
	require.Equal(t, models.ProtocolAnthropic, tr.Protocol())
	require.NoError(t, tr.Ready(context.Background()))
	require.NoError(t, tr.Close(context.Background()))
}
