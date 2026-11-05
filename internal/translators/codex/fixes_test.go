package codex_test

import (
	"context"
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

// I3: пустой стрим Responses → 502 JSON (не 200-пустой-SSE).
func TestI3_EmptyResponsesStream_502(t *testing.T) {
	up := upstream(t, "")
	tr := codex.NewForTest(&models.CodexConfig{}, staticAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "error")
}

// I4: текст после function_call-айтема попадает в СВОЙ text-блок, не в tool_use.
func TestI4_TextAfterFunctionCall_SeparateBlock(t *testing.T) {
	up := upstream(t,
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"bash\",\"call_id\":\"c1\",\"id\":\"it1\"}}\n\n"+
			"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{}\",\"item_id\":\"it1\"}\n\n"+
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"it2\"}}\n\n"+
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"готово\",\"item_id\":\"it2\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	tr := codex.NewForTest(&models.CodexConfig{}, staticAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":9,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	// два content_block_start: tool_use и text — в правильном порядке
	toolPos := strings.Index(out, `"tool_use"`)
	textPos := strings.Index(out, `"type":"text"`)
	require.NotEqual(t, -1, toolPos, "tool_use-блок открыт")
	require.NotEqual(t, -1, textPos, "text-блок открыт")
	require.Less(t, toolPos, textPos, "text-блок идёт после tool_use")
	// text-дельта не попала в блок tool_use: после открытия text-блока
	require.Regexp(t, `(?s)content_block_start.*"type":"text".*text_delta.*готово`, out)
}

// I5: text-блоки закрываются content_block_stop.
func TestI5_TextBlocksClosed(t *testing.T) {
	up := upstream(t,
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"it1\"}}\n\n"+
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"привет\",\"item_id\":\"it1\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
	tr := codex.NewForTest(&models.CodexConfig{}, staticAuth{}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":9,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	starts := strings.Count(out, "event: content_block_start")
	stops := strings.Count(out, "event: content_block_stop")
	require.Equal(t, 1, starts)
	require.Equal(t, stops, starts, "каждый открытый блок закрыт")
}

// I6: рукописный профиль anthropic/openai без protocol-блока — ошибка, не паника.
func TestI6_NilConfigBlockNoPanic(t *testing.T) {
	// проверяется на уровне router factory: см. router-тест; здесь — сами трансляторы
	require.NotPanics(t, func() {
		_ = codex.NewForTest(&models.CodexConfig{}, staticAuth{}, "http://127.0.0.1:1")
	})
}
