package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	codexauth "github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/stretchr/testify/require"
)

type fakeAuth struct{}

func (fakeAuth) Token(_ context.Context) (codexauth.Auth, error) {
	return codexauth.Auth{AccessToken: "acc", RefreshToken: "r", AccountID: "acct-1"}, nil
}

func TestBuildResponsesRequest(t *testing.T) {
	var req models.MessagesRequest
	require.NoError(t, jsonUnmarshal(`{
		"model":"gpt-6.1-codex","max_tokens":100,"system":"you are a coder",
		"messages":[
			{"role":"user","content":"do it"},
			{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash","input":{"cmd":"ls"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}
		],
		"tools":[{"name":"bash","description":"run","input_schema":{"type":"object"}}]
	}`, &req))

	out := codex.BuildResponsesRequest(&req)
	require.Equal(t, "gpt-6.1-codex", out.Model)
	require.Equal(t, "you are a coder", out.Instructions)
	require.Equal(t, 100, out.MaxOutputTokens)
	require.Len(t, out.Input, 3)
	require.Equal(t, "message", out.Input[0].Type)
	require.Equal(t, "function_call", out.Input[1].Type)
	require.Equal(t, "t1", out.Input[1].CallID)
	require.Equal(t, "bash", out.Input[1].Name)
	require.Equal(t, "function_call_output", out.Input[2].Type)
	require.Equal(t, "ok", out.Input[2].Output)
	require.Len(t, out.Tools, 1)
	require.Equal(t, "bash", out.Tools[0].Name)
}

func TestServeMessages_Stream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.CodexConfig{}, fakeAuth{}, up.URL)
	reqBody := `{"model":"gpt-6.1-codex","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "event: message_start")
	require.Contains(t, out, "text_delta")
	require.Contains(t, out, "Hel")
	require.Contains(t, out, "lo")
	require.Contains(t, out, "event: message_stop")
}

func TestServeMessages_FunctionCallStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"bash\",\"call_id\":\"c1\",\"id\":\"it-c1\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"cmd\\\":\",\"item_id\":\"it-c1\"}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.CodexConfig{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "tool_use")
	require.Contains(t, out, "input_json_delta")
}

func TestServeMessages_Headers(t *testing.T) {
	var hdrs http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdrs = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.CodexConfig{Originator: "codex_cli_rs", UserAgent: "codex_cli_rs/1.0"}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, "Bearer acc", hdrs.Get("Authorization"))
	require.Equal(t, "acct-1", hdrs.Get("Chatgpt-Account-Id"))
	require.Equal(t, "codex_cli_rs", hdrs.Get("Originator"))
	require.Equal(t, "codex_cli_rs/1.0", hdrs.Get("User-Agent"))
}

func jsonUnmarshal(raw string, v any) error {
	return json.Unmarshal([]byte(raw), v)
}
