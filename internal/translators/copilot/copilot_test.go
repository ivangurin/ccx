package copilot_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	tr := copilot.NewForTest(&models.CopilotConfig{
		DeviceClientID: "cid",
		IntegrationID:  "copilot-chat",
		EditorVersion:  "vscode/1.99.0",
		EditorPlugin:   "copilot-chat/0.28.0",
		UserAgent:      "GitHubCopilotChat/0.28.0",
		PrefillFix:     ptrBool(true),
	}, staticToken{token: "gho_secret"}, up.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-5.5","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Bearer gho_secret", hdrs.Get("Authorization"))
	require.Equal(t, "2022-11-28", hdrs.Get("X-Github-Api-Version"))
	require.Equal(t, "copilot-chat", hdrs.Get("Copilot-Integration-Id"))
	require.Equal(t, "vscode/1.99.0", hdrs.Get("Editor-Version"))
	require.Equal(t, "GitHubCopilotChat/0.28.0", hdrs.Get("User-Agent"))
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

	tr := copilot.NewForTest(&models.CopilotConfig{PrefillFix: ptrBool(true)}, staticToken{token: "t"}, up.URL)
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

func TestCopilot_PrefillDisabled(t *testing.T) {
	var body string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	t.Cleanup(up.Close)

	tr := copilot.NewForTest(&models.CopilotConfig{PrefillFix: ptrBool(false)}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"m","max_tokens":1,
		"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"pre"}]
	}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.NotContains(t, body, "[continuation]")
	require.Contains(t, body, `"assistant"`)
}

func TestCopilot_Stream(t *testing.T) {
	var hdrs http.Header
	up := capturingUpstream(t, &hdrs, "", true)

	tr := copilot.NewForTest(&models.CopilotConfig{PrefillFix: ptrBool(true)}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":1,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "event: message_start")
	require.Contains(t, out, "ok")
	require.Contains(t, out, "event: message_stop")
}

func TestCopilot_Ready_UsesTokenSource(t *testing.T) {
	tr := copilot.NewForTest(&models.CopilotConfig{}, staticToken{token: "t"}, "http://127.0.0.1:1")
	require.NoError(t, tr.Ready(context.Background()))
}

// I7: a profile without prefill_fix (handwritten) — the fix is still applied
// (default true per the plan; the Copilot gateway rejects prefill without it).
func TestI7_PrefillDefaultTrue(t *testing.T) {
	var body string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	t.Cleanup(up.Close)

	tr := copilot.NewForTest(&models.CopilotConfig{}, staticToken{token: "t"}, up.URL) // PrefillFix not set
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"m","max_tokens":1,
		"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"pre"}]
	}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Contains(t, body, "[continuation]", "the prefill fix is applied by default")
}

func ptrBool(b bool) *bool { return &b }
