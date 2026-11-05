package openai_test

import (
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

// feed прогоняет чанки через конвертер, собирая Anthropic-события.
func feed(t *testing.T, chunks []*models.ChatChunk) []models.AnthropicEvent {
	t.Helper()
	conv := openai.NewStreamConverter("glm")
	events := make([]models.AnthropicEvent, 0, len(chunks)*2)
	for _, c := range chunks {
		events = append(events, conv.Feed(c)...)
	}
	events = append(events, conv.Finish()...)

	return events
}

func chunk(delta models.ChatChunkDelta, finish *string) *models.ChatChunk {
	c := &models.ChatChunk{ID: "chatcmpl-1"}
	c.Choices = append(c.Choices, struct {
		Index        int                   `json:"index"`
		Delta        models.ChatChunkDelta `json:"delta"`
		FinishReason *string               `json:"finish_reason"`
	}{Index: 0, Delta: delta, FinishReason: finish})

	return c
}

func strp(s string) *string { return &s }

const evDeltaBlock = "content_block_delta"

func names(events []models.AnthropicEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}

	return out
}

func TestStream_TextFlow(t *testing.T) {
	events := feed(t, []*models.ChatChunk{
		chunk(models.ChatChunkDelta{Role: "assistant", Content: "При"}, nil),
		chunk(models.ChatChunkDelta{Content: "вет"}, nil),
		chunk(models.ChatChunkDelta{}, strp("stop")),
	})

	require.Equal(t, []string{
		"message_start", "content_block_start", evDeltaBlock, evDeltaBlock,
		"content_block_stop", "message_delta", "message_stop",
	}, names(events))

	// message_start несёт model
	require.Equal(t, "glm", events[0].Message.Model)
	// первая дельта — text_delta
	require.Equal(t, "При", events[2].Delta.Text)
}

func TestStream_FragmentedToolCalls(t *testing.T) {
	first := models.ChatToolCall{ID: "call_1", Type: "function", Function: models.ChatFunction{Name: "bash", Arguments: `{"cmd":`}}
	c1 := chunk(models.ChatChunkDelta{ToolCalls: []models.ChatToolCall{{Index: 0, ID: "call_1", Type: "function", Function: models.ChatFunction{Name: "bash"}}}}, nil)
	_ = first
	c2 := chunk(models.ChatChunkDelta{ToolCalls: []models.ChatToolCall{{Index: 0, Function: models.ChatFunction{Arguments: `{"cmd":`}}}}, nil)
	c3 := chunk(models.ChatChunkDelta{ToolCalls: []models.ChatToolCall{{Index: 0, Function: models.ChatFunction{Arguments: `"ls"}`}}}}, nil)
	events := feed(t, []*models.ChatChunk{c1, c2, chunk(models.ChatChunkDelta{Content: "готовлю вызов"}, nil), c3, chunk(models.ChatChunkDelta{}, strp("tool_calls"))})

	// структура: message_start, text-блок, tool_use-блок, финал
	got := names(events)
	require.Contains(t, got, "content_block_start")
	// tool_use блок: content_block_start c tool_use, input_json_delta-дельты
	var toolStart, jsonDeltas int
	for _, e := range events {
		if e.Type == "content_block_start" && e.ContentBlock != nil && e.ContentBlock.Type == "tool_use" {
			toolStart++
		}
		if e.Type == evDeltaBlock && e.Delta != nil && e.Delta.Type == "input_json_delta" {
			jsonDeltas++
		}
	}
	require.Equal(t, 1, toolStart)
	require.Equal(t, 2, jsonDeltas)
	// stop_reason tool_use
	var lastDelta *models.AnthropicEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			lastDelta = &events[i]
		}
	}
	require.NotNil(t, lastDelta)
	require.Equal(t, "tool_use", lastDelta.Delta.StopReason)
}

func TestStream_ReasoningToThinking(t *testing.T) {
	events := feed(t, []*models.ChatChunk{
		chunk(models.ChatChunkDelta{Reasoning: "думаю"}, nil),
		chunk(models.ChatChunkDelta{Content: "ответ"}, nil),
		chunk(models.ChatChunkDelta{}, strp("stop")),
	})

	var thinkingStart, thinkingDelta bool
	for _, e := range events {
		if e.Type == "content_block_start" && e.ContentBlock != nil && e.ContentBlock.Type == "thinking" {
			thinkingStart = true
		}
		if e.Type == evDeltaBlock && e.Delta != nil && e.Delta.Type == "thinking_delta" {
			thinkingDelta = true
		}
	}
	require.True(t, thinkingStart, "thinking-блок не открыт")
	require.True(t, thinkingDelta, "thinking_delta не пришла")
}

func TestStream_UsageFromFinalChunk(t *testing.T) {
	c := chunk(models.ChatChunkDelta{}, strp("stop"))
	c.Usage = &models.Usage{InputTokens: 7, OutputTokens: 3}
	events := feed(t, []*models.ChatChunk{c})

	var msgDelta *models.AnthropicEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			msgDelta = &events[i]
		}
	}
	require.NotNil(t, msgDelta)
	require.Equal(t, 3, msgDelta.Usage.OutputTokens)
}
