package codex_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/stretchr/testify/require"
)

// Claude Code runs WebSearch via a server-side tool: the internal search
// request carries {"type":"web_search_20250305","name":"web_search"} and
// expects the server to execute the search. The translator maps it onto the
// Responses API hosted {"type":"web_search"} tool (verified live: the ChatGPT
// backend accepts it and streams web_search_call + url_citation events).
// Other server tools have no hosted counterpart: they are dropped instead of
// being mangled into phantom functions nobody executes.
func TestBuildResponsesRequest_WebSearchServerTool(t *testing.T) {
	var req models.MessagesRequest
	require.NoError(t, jsonUnmarshal(`{
		"model":"gpt-6.1-sol","max_tokens":10,
		"messages":[{"role":"user","content":"search the web"}],
		"tools":[
			{"type":"web_search_20250305","name":"web_search","max_uses":8},
			{"name":"bash","description":"run","input_schema":{"type":"object"}},
			{"type":"computer_20250124","name":"computer"}
		]
	}`, &req))

	out := codex.BuildResponsesRequest(&req)

	require.Len(t, out.Tools, 2)
	require.Equal(t, "web_search", out.Tools[0].Type)
	require.Empty(t, out.Tools[0].Name)
	require.Equal(t, "function", out.Tools[1].Type)
	require.Equal(t, "bash", out.Tools[1].Name)

	// the hosted tool must serialize bare — the backend validates strictly
	enc, err := json.Marshal(out.Tools[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"web_search"}`, string(enc))
}

// A response with hosted web search (web_search_call items + url_citation
// annotations) is translated back into the Anthropic server-tool blocks
// Claude Code's WebSearch parses: server_tool_use per search plus one
// aggregated web_search_tool_result with the deduplicated citations. Block
// fields land unescaped in the event JSON; the content items are nested
// JSON, hence the escaped quotes in the assertions.
func TestServeMessages_WebSearchStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"ws_1\",\"type\":\"web_search_call\",\"status\":\"in_progress\"}}\n\n",
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ws_1\",\"type\":\"web_search_call\",\"status\":\"completed\",\"action\":{\"type\":\"search\",\"query\":\"gpt-6 news\"}}}\n\n",
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\"}}\n\n",
			"event: response.output_text.annotation.added\ndata: {\"type\":\"response.output_text.annotation.added\",\"annotation\":{\"type\":\"url_citation\",\"url\":\"https://a.example/1\",\"title\":\"A One\",\"start_index\":0,\"end_index\":5}}\n\n",
			"event: response.output_text.annotation.added\ndata: {\"type\":\"response.output_text.annotation.added\",\"annotation\":{\"type\":\"url_citation\",\"url\":\"https://a.example/1\",\"title\":\"A One (dup)\",\"start_index\":6,\"end_index\":9}}\n\n",
			"event: response.output_text.annotation.added\ndata: {\"type\":\"response.output_text.annotation.added\",\"annotation\":{\"type\":\"url_citation\",\"url\":\"https://b.example/2\",\"title\":\"B Two\",\"start_index\":10,\"end_index\":15}}\n\n",
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"summary\",\"item_id\":\"msg_1\"}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":9,\"output_tokens\":7}}}\n\n",
		}
		for _, ev := range events {
			_, _ = io.WriteString(w, ev)
		}
	}))
	t.Cleanup(up.Close)

	tr := codex.NewForTest(&models.Profile{}, fakeAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "summary") // the text answer survives
	require.Contains(t, out, `"type":"server_tool_use"`)
	require.Contains(t, out, `"name":"web_search"`)
	require.Contains(t, out, `"id":"ws_1"`)
	// json.RawMessage fields inline into the event JSON unescaped
	require.Contains(t, out, `"query":"gpt-6 news"`)
	require.Contains(t, out, `"type":"web_search_tool_result"`)
	require.Contains(t, out, `"tool_use_id":"ws_1"`)
	require.Contains(t, out, `"type":"search_result"`)
	require.Contains(t, out, `"source":"https://a.example/1"`)
	require.Contains(t, out, `"title":"A One"`)
	require.Equal(t, 2, strings.Count(out, `"type":"search_result"`), "duplicate citation must be deduped")
	require.NotContains(t, out, "A One (dup)", "first title wins on dedupe")
	require.Contains(t, out, `"source":"https://b.example/2"`)
	require.Contains(t, out, "event: message_stop")
	require.NotContains(t, out, "event: error")
}
