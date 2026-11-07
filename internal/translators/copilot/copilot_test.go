package copilot_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/copilot"
	"github.com/stretchr/testify/require"
)

type staticToken struct{ token string }

func (s staticToken) Token(_ context.Context) (string, error) { return s.token, nil }

// capturingUpstream captures headers, serves a canned response.
func capturingUpstream(t *testing.T, captured *http.Header, body string, isSSE bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.Header.Clone()
		if isSSE {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestCopilot_MasqueradeHeaders(t *testing.T) {
	var hdrs http.Header
	up := capturingUpstream(t, &hdrs, `{"id":"c","choices":[{"message":{"content":"x"},"finish_reason":"stop"}],"usage":{}}`, false)

	tr := copilot.NewForTest(&models.Profile{
		Protocol: models.ProtocolCopilot,
	}, staticToken{token: "gho_secret"}, up.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-5.5","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Bearer gho_secret", hdrs.Get("Authorization"))
	// No X-Github-Api-Version: it is a github.com REST header — the Copilot
	// chat gateway answers it with "bad request: invalid apiVersion" (live).
	require.Empty(t, hdrs.Get("X-Github-Api-Version"))
	require.Equal(t, copilot_client.IntegrationID, hdrs.Get("Copilot-Integration-Id"))
	require.Equal(t, copilot_client.EditorVersion, hdrs.Get("Editor-Version"))
	require.Equal(t, copilot_client.MasqueradeUA, hdrs.Get("User-Agent"))
}

func TestCopilot_PrefillRewritten(t *testing.T) {
	var body string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	t.Cleanup(up.Close)

	tr := copilot.NewForTest(&models.Profile{}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet-5.5","max_tokens":10,
		"messages":[{"role":"user","content":"write"},{"role":"assistant","content":[{"type":"text","text":"hal"}]}]
	}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	// the assistant prefill did not go upstream: the finale is a user message with an instruction
	require.Contains(t, body, "[continuation]")
	require.Contains(t, body, "hal")
	require.NotContains(t, body, `"role":"assistant"`)
}

func TestCopilot_Stream(t *testing.T) {
	var hdrs http.Header
	up := capturingUpstream(t, &hdrs, "", true)

	tr := copilot.NewForTest(&models.Profile{}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":1,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "event: message_start")
	require.Contains(t, out, "ok")
	require.Contains(t, out, "event: message_stop")
}

func TestCopilot_Ready_UsesTokenSource(t *testing.T) {
	tr := copilot.NewForTest(&models.Profile{}, staticToken{token: "t"}, "http://127.0.0.1:1")
	require.NoError(t, tr.Ready(context.Background()))
}

// An upstream error is relayed as is (status + JSON body).
func TestCopilot_UpstreamErrorRelayed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"denied"}`)
	}))
	t.Cleanup(up.Close)

	tr := copilot.NewForTest(&models.Profile{}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"denied"}`, rec.Body.String())
}
