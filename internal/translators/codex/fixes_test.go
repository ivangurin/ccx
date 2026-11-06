package codex_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	codexauth "github.com/ivangurin/ccms/internal/clients/codex"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/translators/codex"
	"github.com/stretchr/testify/require"
)

type staticAuth struct{}

func (staticAuth) Token(_ context.Context) (codexauth.Auth, error) {
	return codexauth.Auth{AccessToken: "a", RefreshToken: "r", AccountID: "acct"}, nil
}

func upstream(t *testing.T, events string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, events)
	}))
	t.Cleanup(srv.Close)

	return srv
}

// I3: an empty Responses stream → 502 JSON (not a 200 empty SSE).
func TestI3_EmptyResponsesStream_502(t *testing.T) {
	up := upstream(t, "")
	tr := codex.NewForTest(&models.Profile{}, staticAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "error")
}

// I4: text after a function_call item goes into its OWN text block, not into tool_use.
func TestI4_TextAfterFunctionCall_SeparateBlock(t *testing.T) {
	up := upstream(t,
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"bash\",\"call_id\":\"c1\",\"id\":\"it1\"}}\n\n"+
			"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{}\",\"item_id\":\"it1\"}\n\n"+
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"it2\"}}\n\n"+
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"done\",\"item_id\":\"it2\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	tr := codex.NewForTest(&models.Profile{}, staticAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":9,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	// two content_block_start events: tool_use and text — in the right order
	toolPos := strings.Index(out, `"tool_use"`)
	textPos := strings.Index(out, `"type":"text"`)
	require.NotEqual(t, -1, toolPos, "tool_use block opened")
	require.NotEqual(t, -1, textPos, "text block opened")
	require.Less(t, toolPos, textPos, "the text block comes after tool_use")
	// the text delta did not land in the tool_use block: after the text block opens
	require.Regexp(t, `(?s)content_block_start.*"type":"text".*text_delta.*done`, out)
}

// I5: text blocks are closed with content_block_stop.
func TestI5_TextBlocksClosed(t *testing.T) {
	up := upstream(t,
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"it1\"}}\n\n"+
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"item_id\":\"it1\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
	tr := codex.NewForTest(&models.Profile{}, staticAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":9,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	starts := strings.Count(out, "event: content_block_start")
	stops := strings.Count(out, "event: content_block_stop")
	require.Equal(t, 1, starts)
	require.Equal(t, stops, starts, "every opened block is closed")
}

// I6: a handwritten anthropic/openai profile without a protocol block — an error, not a panic.
func TestI6_NilConfigBlockNoPanic(t *testing.T) {
	// checked at the router factory level: see the router test; here — the translators themselves
	require.NotPanics(t, func() {
		_ = codex.NewForTest(&models.Profile{}, staticAuth{}, "http://127.0.0.1:1")
	})
}
