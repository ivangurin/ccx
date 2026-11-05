package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/chatstream"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

// I1: живой OpenAI шлёт prompt_tokens/completion_tokens.
func TestI1_StreamUsage_OpenAIKeyNames(t *testing.T) {
	c := &models.ChatChunk{}
	c.Choices = append(c.Choices, struct {
		Index        int                   `json:"index"`
		Delta        models.ChatChunkDelta `json:"delta"`
		FinishReason *string               `json:"finish_reason"`
	}{FinishReason: strp("stop")})
	c.Usage = &models.Usage{InputTokens: 100, OutputTokens: 50} // значения заданы напрямую…

	// …а теперь проверим сам парсинг JSON-ключей продового формата:
	var parsed models.ChatChunk
	require.NoError(t, parseJSON(`{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":50}}`, &parsed))
	require.NotNil(t, parsed.Usage)
	require.Equal(t, 100, parsed.Usage.InputTokens, "prompt_tokens должен маппиться в InputTokens")
	require.Equal(t, 50, parsed.Usage.OutputTokens, "completion_tokens → OutputTokens")
	_ = c
	_ = strp
}

// I2: обрыв стрима апстрима = ошибка, а не тихий end_turn.
func TestI2_UpstreamStreamBreak_EmitsError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"полу\"}}]}\n\n")
		// [DONE] не пришёл — соединение просто закрылось
	}))
	t.Cleanup(up.Close)

	tr := openai.New(&models.Profile{
		Protocol: models.ProtocolOpenAI,
		OpenAI:   &models.OpenAIConfig{BaseURL: up.URL},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	out := rec.Body.String()
	require.Contains(t, out, "полу", "успевшие дельты дошли")
	require.Contains(t, out, "event: error", "обрыв = SSE-error")
	require.NotContains(t, out, `"stop_reason":"end_turn"`, "обрыв не маскируется под нормальное завершение")
}

// I3: пустой стрим апстрима (200 + ноль событий) → 502, а не пустой SSE.
func TestI3_EmptyUpstreamStream_502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "") // сразу EOF
	}))
	t.Cleanup(up.Close)

	tr := openai.New(&models.Profile{
		Protocol: models.ProtocolOpenAI,
		OpenAI:   &models.OpenAIConfig{BaseURL: up.URL},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":5,"stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "error")
}

// I5(б): Finish закрывает блоки в порядке открытия (детерминизм).
func TestI5_BlockStopOrderDeterministic(t *testing.T) {
	for range 5 {
		conv := chatstream.NewStreamConverter("m")
		events := make([]models.AnthropicEvent, 0, 8)
		events = append(events, conv.Feed(chunk(models.ChatChunkDelta{Content: "a"}, nil))...)
		events = append(events, conv.Feed(chunk(models.ChatChunkDelta{ToolCalls: []models.ChatToolCall{{Index: 0, ID: "t", Type: "function", Function: models.ChatFunction{Name: "n"}}}}, nil))...)
		events = append(events, conv.Feed(chunk(models.ChatChunkDelta{ToolCalls: []models.ChatToolCall{{Index: 1, ID: "t2", Type: "function", Function: models.ChatFunction{Name: "n2"}}}}, nil))...)
		events = append(events, conv.Finish()...)

		var stops []int
		for _, e := range events {
			if e.Type == "content_block_stop" && e.Index != nil {
				stops = append(stops, *e.Index)
			}
		}
		require.Equal(t, []int{0, 1, 2}, stops, "порядок закрытия блоков = порядок открытия")
	}
}

// I5(в): message_stop без payload — строго {"type":"message_stop"}.
func TestI5_MessageStopNoPayload(t *testing.T) {
	events := feed(t, []*models.ChatChunk{chunk(models.ChatChunkDelta{Content: "x"}, strp("stop"))})
	last := events[len(events)-1]
	require.Equal(t, "message_stop", last.Type)

	data, err := marshalJSON(last)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"message_stop"}`, string(data))
}

func strp(s string) *string { return &s }

func chunk(delta models.ChatChunkDelta, finish *string) *models.ChatChunk {
	c := &models.ChatChunk{ID: "fix"}
	c.Choices = append(c.Choices, struct {
		Index        int                   `json:"index"`
		Delta        models.ChatChunkDelta `json:"delta"`
		FinishReason *string               `json:"finish_reason"`
	}{Index: 0, Delta: delta, FinishReason: finish})

	return c
}

func feed(t *testing.T, chunks []*models.ChatChunk) []models.AnthropicEvent {
	t.Helper()
	conv := chatstream.NewStreamConverter("m")
	events := make([]models.AnthropicEvent, 0, len(chunks)*2)
	for _, c := range chunks {
		events = append(events, conv.Feed(c)...)
	}
	events = append(events, conv.Finish()...)

	return events
}

func parseJSON(raw string, v any) error {
	return jsonDecodeString(raw, v)
}

func marshalJSON(v any) ([]byte, error) {
	return jsonEncode(v)
}

func jsonDecodeString(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }

func jsonEncode(v any) ([]byte, error) { return json.Marshal(v) }
