package codex_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/stretchr/testify/require"
)

// responsesSSE answers a full Responses SSE sequence: one text item with two
// deltas, one function_call with its arguments delta, then response.completed
// with usage. The upstream stream ends with the connection close (no [DONE] —
// that is a chat/completions convention).
func responsesSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	seq := []string{
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"type\":\"message\",\"role\":\"assistant\"}}\n\n",
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"item_1\",\"delta\":\"Hello\"}\n\n",
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"item_1\",\"delta\":\" world\"}\n\n",
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_2\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"get_weather\"}}\n\n",
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_2\",\"delta\":\"{\\\"city\\\":\\\"SF\\\"}\"}\n\n",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":7}}}\n\n",
	}
	for _, ev := range seq {
		_, _ = io.WriteString(w, ev)
	}
}

// Non-stream requests are served by streaming upstream and aggregating: the
// backend contract is stream-only, so the upstream body carries stream:true
// and the client gets one JSON MessagesResponse with the complete content.
func TestCodex_NonStreamAggregates(t *testing.T) {
	var upstreamBody []byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		responsesSSE(w)
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-5","max_tokens":50,"stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	var sent map[string]any
	require.NoError(t, json.Unmarshal(upstreamBody, &sent))
	require.Equal(t, true, sent["stream"], "upstream is always stream — the backend contract")

	require.Equal(t, http.StatusOK, rec.Code)
	var out models.MessagesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "message", out.Type)
	require.Equal(t, "assistant", out.Role)
	require.Len(t, out.Content, 2)
	require.Equal(t, "text", out.Content[0].Type)
	require.NotNil(t, out.Content[0].Text)
	require.Equal(t, "Hello world", *out.Content[0].Text)
	require.Equal(t, "tool_use", out.Content[1].Type)
	require.NotNil(t, out.Content[1].Input)
	require.JSONEq(t, `{"city":"SF"}`, string(out.Content[1].Input))
	require.Equal(t, "get_weather", *out.Content[1].Name)
	require.NotNil(t, out.StopReason)
	require.Equal(t, "tool_use", *out.StopReason)
	require.Equal(t, 10, out.Usage.InputTokens)
	require.Equal(t, 7, out.Usage.OutputTokens)
}

// The parallel-call wire shape (no argument deltas, arguments carried by the
// done events — see TestServeMessages_ParallelFunctionCallArgumentsWithoutDeltas)
// must fold into complete tool_use inputs on the aggregated path too, not
// into literal {} objects.
func TestCodex_NonStreamParallelCallsWithoutDeltas(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		seq := []string{
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_a\",\"name\":\"echo\",\"arguments\":\"\"}}\n\n",
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_a\",\"name\":\"echo\",\"arguments\":\"{\\\"message\\\":\\\"hi\\\"}\",\"status\":\"completed\"}}\n\n",
			"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"fc_1\",\"arguments\":\"{\\\"message\\\":\\\"hi\\\"}\"}\n\n",
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_b\",\"name\":\"add\",\"arguments\":\"\"}}\n\n",
			"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"fc_2\",\"arguments\":\"{\\\"a\\\":2,\\\"b\\\":40}\"}\n\n",
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_b\",\"name\":\"add\",\"arguments\":\"{\\\"a\\\":2,\\\"b\\\":40}\",\"status\":\"completed\"}}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":9,\"output_tokens\":5}}}\n\n",
		}
		for _, ev := range seq {
			_, _ = io.WriteString(w, ev)
		}
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-5","max_tokens":50,"stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var out models.MessagesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Content, 2)
	require.Equal(t, "tool_use", out.Content[0].Type)
	require.JSONEq(t, `{"message":"hi"}`, string(out.Content[0].Input))
	require.Equal(t, "tool_use", out.Content[1].Type)
	require.JSONEq(t, `{"a":2,"b":40}`, string(out.Content[1].Input))
}

// A response.failed event in the aggregated stream must surface as 502, not
// as a partial message that looks complete.
func TestCodex_NonStreamFailedEventIs502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"model overloaded\"}}}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-5","max_tokens":50,"stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "model overloaded")
}

// The Responses "error" event carries its text in the message field (not a
// delta) — the relayed error must say what the backend said.
func TestCodex_NonStreamErrorEventCarriesMessage(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"The server had an error processing your request\"}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-5","max_tokens":50,"stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "The server had an error processing your request")
}

// The aggregated path records TTFT too — the upstream is a stream, its first
// event time is available and the usage log should not show a systematic 0.
func TestCodex_NonStreamRecordsTTFT(t *testing.T) {
	capt := &usageCapture{}
	old := usagelog.Default
	usagelog.Default = capt
	t.Cleanup(func() { usagelog.Default = old })

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{Name: "sub"}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"gpt-5","max_tokens":50,"stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	e := capt.last(t)
	require.Positive(t, e.TTFTMS)
}

type usageCapture struct {
	mu      sync.Mutex
	entries []usagelog.Entry
}

func (c *usageCapture) Record(e usagelog.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *usageCapture) last(t *testing.T) usagelog.Entry {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.NotEmpty(t, c.entries)

	return c.entries[len(c.entries)-1]
}
