package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	codexauth "github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/reqdump"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/stretchr/testify/require"
)

type fakeAuth struct{}

func (fakeAuth) Token(_ context.Context) (codexauth.Auth, error) {
	return codexauth.Auth{AccessToken: "acc", RefreshToken: "r", AccountID: "acct-1"}, nil
}

// dumpLinesOf — the decoded unified-log lines a capture holds: dumps ride
// the daemon logger as kind=reqdump lines (no file anymore), and the
// external test package reads back the decoded shape.
func dumpLinesOf(t *testing.T, c *suite_provider.Capture) []map[string]any {
	t.Helper()
	lines := c.Lines(t)
	require.NotEmpty(t, lines, "no dump lines captured")

	return lines
}

// dumpLineOf — the decoded line whose dump_kind matches (fails otherwise).
func dumpLineOf(t *testing.T, lines []map[string]any, kind string) map[string]any {
	t.Helper()
	for _, l := range lines {
		if l["dump_kind"] == kind {
			return l
		}
	}
	t.Fatalf("no dump line with dump_kind %s", kind)

	return nil
}

// dumpBodyRaw — the body of one decoded dump line re-marshaled (a valid
// JSON body embeds verbatim as an object; a non-JSON one as a string).
func dumpBodyRaw(t *testing.T, line map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(line["body"])
	require.NoError(t, err)

	return raw
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
	require.NotNil(t, out.Input[2].Output)
	require.Equal(t, "ok", *out.Input[2].Output)
	require.Len(t, out.Tools, 1)
	require.Equal(t, "bash", out.Tools[0].Name)
}

// Non-stream requests are served by upstream streaming + aggregation — see
// aggregate_test.go (TestCodex_NonStreamAggregates).

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

// Parallel function calls arrive WITHOUT any function_call_arguments.delta
// events: the ChatGPT backend carries the arguments only in
// response.function_call_arguments.done (top-level arguments string) and in
// the complete output_item.done item — in either order (verified live
// 2026-11-08: a parallel Claude Code Agent launch reached the client with
// input {} and its validator rejected the missing required params, while
// single calls do stream classic deltas). Both carriers must feed the
// tool_use input, exactly once per item.
func TestServeMessages_ParallelFunctionCallArgumentsWithoutDeltas(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		seq := []string{
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_a\",\"name\":\"echo\",\"arguments\":\"\"}}\n\n",
			"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"fc_1\",\"arguments\":\"{\\\"message\\\":\\\"hi\\\"}\"}\n\n",
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_a\",\"name\":\"echo\",\"arguments\":\"{\\\"message\\\":\\\"hi\\\"}\",\"status\":\"completed\"}}\n\n",
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_b\",\"name\":\"add\",\"arguments\":\"\"}}\n\n",
			// this call's item.done arrives BEFORE its arguments.done (observed live)
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_b\",\"name\":\"add\",\"arguments\":\"{\\\"a\\\":2,\\\"b\\\":40}\",\"status\":\"completed\"}}\n\n",
			"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"fc_2\",\"arguments\":\"{\\\"a\\\":2,\\\"b\\\":40}\"}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":9,\"output_tokens\":5}}}\n\n",
		}
		for _, ev := range seq {
			_, _ = io.WriteString(w, ev)
		}
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"m","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, `"id":"call_a"`)
	require.Contains(t, out, `"id":"call_b"`)
	require.Contains(t, out, `"partial_json":"{\"message\":\"hi\"}"`)
	require.Contains(t, out, `"partial_json":"{\"a\":2,\"b\":40}"`)
	// exactly one delivery per call — the second carrier of the same arguments
	// must not append a second copy of the JSON
	require.Equal(t, 2, strings.Count(out, "input_json_delta"))
	require.Contains(t, out, `"stop_reason":"tool_use"`)
}

// Classic single-call emission — delta fragments followed by done carriers
// carrying the same full string: the fragments already delivered the input,
// so the done carriers must be ignored, not appended (input_json_delta
// fragments concatenate on the client).
func TestServeMessages_FunctionCallDoneDoesNotDuplicateDeltaArguments(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		seq := []string{
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_a\",\"name\":\"bash\",\"arguments\":\"\"}}\n\n",
			"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_1\",\"delta\":\"{\\\"cmd\\\":\"}\n\n",
			"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_1\",\"delta\":\"\\\"ls\\\"}\"}\n\n",
			"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"fc_1\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}\n\n",
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_a\",\"name\":\"bash\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\",\"status\":\"completed\"}}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n",
		}
		for _, ev := range seq {
			_, _ = io.WriteString(w, ev)
		}
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"m","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	// the two fragments pass through, the two done carriers do not
	require.Equal(t, 2, strings.Count(out, "input_json_delta"))
	require.Contains(t, out, `"partial_json":"{\"cmd\":"`)
	require.Contains(t, out, `"partial_json":"\"ls\"}"`)
	require.NotContains(t, out, `"partial_json":"{\"cmd\":\"ls\"}"`)
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
	require.Equal(t, "codex_cli_rs/0.159.0", hdrs.Get("User-Agent"))
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

// A tool_use with no input (the client omits the field when nothing streamed)
// must serialize as arguments:"{}", not as an omitted field: omitempty would
// drop empty-string arguments and the backend rejects the item outright
// ("Missing required parameter: 'input[1].arguments'", verified live
// 2026-11-08 — the poisoned history after a lost-arguments round 400'd every
// retry until the conversation was abandoned).
func TestBuildResponsesRequest_NilInputIsNotEmptyArguments(t *testing.T) {
	var req models.MessagesRequest
	require.NoError(t, jsonUnmarshal(`{
		"model":"gpt-6.1-sol","max_tokens":10,
		"messages":[
			{"role":"user","content":"do it"},
			{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash"}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}
		],
		"tools":[{"name":"bash","description":"run","input_schema":{"type":"object"}}]
	}`, &req))

	raw, err := json.Marshal(codex.BuildResponsesRequest(&req))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"arguments":"{}"`)
}

// A successful stream also dumps its raw upstream SSE events (codex-sse):
// wire-shape surprises — like the parallel-call arguments delivery — are only
// visible on the real backend, and the paired dumps are what diagnoses them.
func TestServeMessages_DumpsUpstreamSSEWhenEnabled(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
	}))
	t.Cleanup(up.Close)

	capture := &suite_provider.Capture{}
	reqdump.ResetForTest()
	t.Cleanup(reqdump.ResetForTest)
	// dumping is --debug-gated in production (app.Run Configure); the
	// translator test arms it directly with a capture logger (dumps ride the
	// unified log as kind=reqdump lines, no file anymore)
	reqdump.Configure(reqdump.Options{Enabled: true, Logger: slog.New(slog.NewJSONHandler(capture, nil))})

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"x"}]}`))
	tr.ServeMessages(httptest.NewRecorder(), req)

	require.Contains(t, capture.String(), `"dump_kind":"codex-sse"`)
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
// through the unified logger — the pair (what we sent / why it was rejected)
// is the artifact live debugging needs.
func TestServeMessages_DumpsExchangeWhenEnabled(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"detail":"System messages are not allowed"}`)
	}))
	t.Cleanup(up.Close)

	capture := &suite_provider.Capture{}
	reqdump.ResetForTest()
	t.Cleanup(reqdump.ResetForTest)
	reqdump.Configure(reqdump.Options{Enabled: true, Logger: slog.New(slog.NewJSONHandler(capture, nil))})

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","system":"s","stream":true,"messages":[{"role":"user","content":"x"}]}`))
	tr.ServeMessages(httptest.NewRecorder(), req)

	lines := dumpLinesOf(t, capture)
	require.Len(t, lines, 2)
	reqDump := dumpLineOf(t, lines, "codex-req")
	respDump := dumpLineOf(t, lines, "codex-resp-400")
	require.Contains(t, string(dumpBodyRaw(t, reqDump)), `"instructions":"s"`)
	require.Contains(t, string(dumpBodyRaw(t, reqDump)), `"store":false`)
	require.JSONEq(t, `{"detail":"System messages are not allowed"}`, string(dumpBodyRaw(t, respDump)))
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

// An empty tool_result content must still serialize function_call_output
// WITH its output field: the backend rejects the omission
// ("Missing required parameter: 'input[N].output'", verified live
// 2026-11-08 — replaying a history whose Agent calls returned no content).
// The output twin of the arguments:"{}" normalization.
func TestBuildResponsesRequest_EmptyToolResultKeepsOutputField(t *testing.T) {
	var req models.MessagesRequest
	require.NoError(t, jsonUnmarshal(`{
		"model":"gpt-6.1-sol","max_tokens":10,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Agent","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":""}]}
		]
	}`, &req))

	out := codex.BuildResponsesRequest(&req)
	raw, err := json.Marshal(out.Input[1])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"output":""`) // present, not omitted
}

// A stream that ends (clean EOF) WITHOUT a terminal event
// (response.completed/failed/incomplete) was truncated upstream — observed
// live 2026-11-08 17:42-17:45: every codex request returned 200 + an EMPTY
// assistant message (in=0/out=0 in the usage log), which Claude Code renders
// as "no visible output" and retries into the same wall. A truncation must
// surface as an error event, never as a healthy empty answer.
func TestServeMessages_StreamEndsWithoutCompletionSurfacesError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"par\"}\n\n")
		// no response.completed — the connection just closes
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "text_delta") // the partial content did relay
	require.Contains(t, out, "event: error")
	require.Contains(t, out, "ended without completion")
	require.Contains(t, out, "event: message_stop") // the stream still closes cleanly
}

// response.incomplete IS a terminal event (the documented Responses API
// third terminal, e.g. content_filter): its usage must be captured and the
// truncation surfaced as an error — an incomplete answer is not an answer.
func TestServeMessages_ResponseIncompleteSurfacesError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"half \"}\n\n")
		_, _ = io.WriteString(w, "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":7,\"output_tokens\":3},\"incomplete_details\":{\"reason\":\"content_filter\"}}}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "event: error")
	require.Contains(t, out, "response incomplete")
	require.Contains(t, out, "content_filter") // the reason rides along
	require.Contains(t, out, `"input_tokens":7`)
	require.Contains(t, out, `"output_tokens":3`) // usage still recorded
}
