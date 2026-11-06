package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codexauth "github.com/ivangurin/ccms/internal/clients/codex"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/translators/codex"
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

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
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
	// The Responses stream ends with the connection close (no [DONE], unlike
	// chat/completions) — a clean EOF must not surface as an error event.
	require.NotContains(t, out, "event: error")
}

func TestServeMessages_FunctionCallStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"bash\",\"call_id\":\"c1\",\"id\":\"it-c1\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"cmd\\\":\",\"item_id\":\"it-c1\"}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
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

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, "Bearer acc", hdrs.Get("Authorization"))
	require.Equal(t, "acct-1", hdrs.Get("Chatgpt-Account-Id"))
	// the CLI mimicry is a constant, not profile config
	require.Equal(t, "codex_cli_rs", hdrs.Get("Originator"))
	require.Equal(t, "codex_cli_rs/1.0.0", hdrs.Get("User-Agent"))
}

func jsonUnmarshal(raw string, v any) error {
	return json.Unmarshal([]byte(raw), v)
}

// An upstream error is relayed as is (already JSON — status + body), and
// count_tokens answers locally even with an empty body.
func TestServeMessages_UpstreamErrorRelayed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad token"}}`)
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":{"message":"bad token"}}`, rec.Body.String())

	rec2 := httptest.NewRecorder()
	tr.ServeCountTokens(rec2, httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil))
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Contains(t, rec2.Body.String(), "input_tokens")
}

// A function_call item must not carry item_id: the ChatGPT backend validates
// input params strictly ("Unknown parameter: 'input[5].item_id'", verified
// live). item_id exists only in SSE events; on the request side call_id alone
// ties a call to its output.
func TestBuildResponsesRequest_FunctionCallHasNoItemID(t *testing.T) {
	var req models.MessagesRequest
	require.NoError(t, jsonUnmarshal(`{
		"model":"gpt-6.1-sol","max_tokens":10,
		"messages":[
			{"role":"user","content":"do it"},
			{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash","input":{"cmd":"ls"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}
		],
		"tools":[{"name":"bash","description":"run","input_schema":{"type":"object"}}]
	}`, &req))

	raw, err := json.Marshal(codex.BuildResponsesRequest(&req))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "item_id")
	// the call↔output tie survives via call_id
	require.Contains(t, string(raw), `"call_id":"t1"`)
}

// Claude Code sends SessionStart-hook output as a message with role "system"
// inside messages[] (not the top-level system field); the ChatGPT backend
// rejects input items with that role ("System messages are not allowed",
// verified live). Such messages merge into instructions instead — the same
// channel Codex CLI uses for system-level text — and never reach input.
func TestBuildResponsesRequest_SystemRoleMergesIntoInstructions(t *testing.T) {
	var req models.MessagesRequest
	require.NoError(t, jsonUnmarshal(`{
		"model":"gpt-6.1-sol","max_tokens":10,"system":"base prompt",
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"system","content":"hook additional context"},
			{"role":"user","content":"go on"}
		]
	}`, &req))

	out := codex.BuildResponsesRequest(&req)

	require.Equal(t, "base prompt\n\nhook additional context", out.Instructions)
	require.Len(t, out.Input, 2)
	for _, item := range out.Input {
		require.NotEqual(t, "system", item.Role)
	}
	require.Equal(t, "user", out.Input[0].Role)
	require.Equal(t, "user", out.Input[1].Role)
}

// The outgoing Responses payload and the rejected upstream body are dumped
// under CCMS_DUMP_DIR — the pair (what we sent / why it was rejected) is the
// artifact live debugging needs.
func TestServeMessages_DumpsExchangeWhenEnabled(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"detail":"System messages are not allowed"}`)
	}))
	t.Cleanup(up.Close)

	dir := filepath.Join(t.TempDir(), "dumps")
	t.Setenv("CCMS_DUMP_DIR", dir)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","system":"s","stream":true,"messages":[{"role":"user","content":"x"}]}`))
	tr.ServeMessages(httptest.NewRecorder(), req)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	dumped := map[string]string{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // test sandbox dir
		require.NoError(t, err)
		dumped[e.Name()] = string(data)
	}
	var reqDump, respDump string
	for name, body := range dumped {
		switch {
		case strings.Contains(name, "codex-req"):
			reqDump = body
		case strings.Contains(name, "codex-resp-400"):
			respDump = body
		default:
			t.Fatalf("unexpected dump file: %s", name)
		}
	}
	require.Contains(t, reqDump, `"instructions":"s"`)
	require.Contains(t, reqDump, `"store":false`)
	require.JSONEq(t, `{"detail":"System messages are not allowed"}`, respDump)
}

// The ChatGPT backend rejects the request without an explicit "store":
// false ("Store must be set to false" — verified live); Codex CLI always
// sends it. The field must serialize at its zero value — no omitempty.
func TestResponsesRequest_StoreAlwaysFalse(t *testing.T) {
	var req models.MessagesRequest
	require.NoError(t, jsonUnmarshal(`{"model":"gpt-6.1-sol","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, &req))

	raw, err := json.Marshal(codex.BuildResponsesRequest(&req))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"store":false`)
}
