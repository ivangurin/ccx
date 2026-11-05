package chatstream

import (
	"slices"

	"github.com/ivangurin/ccx/internal/models"
)

// имена событий (goconst).
const evMessageDelta = "message_delta"

// stopReason — finish_reason → stop_reason.
func stopReason(finish string) string {
	switch finish {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	default: // stop и неизвестные
		return "end_turn"
	}
}

// StreamConverter превращает SSE-чанки chat/completions в события
// Anthropic Messages. Состояние: текущий блок (text/thinking/tool_use per index),
// финальный stop_reason/usage. Чистый конвертер — golden-тестируем.
type StreamConverter struct {
	model     string
	started   bool
	nextIndex int
	// открытые блоки: tool_use по openai-index → anthropic-index
	toolBlocks map[int]int
	usage      *models.Usage
	stopReason string
}

// NewStreamConverter создаёт конвертер для модели.
func NewStreamConverter(model string) *StreamConverter {
	return &StreamConverter{model: model, toolBlocks: map[int]int{}}
}

// Feed принимает чанк, возвращает события.
func (c *StreamConverter) Feed(chunk *models.ChatChunk) []models.AnthropicEvent {
	var events []models.AnthropicEvent
	if !c.started {
		c.started = true
		events = append(events, models.AnthropicEvent{
			Type: "message_start",
			Message: &models.AnthropicMessagePayload{
				ID: chunk.ID, Type: "message", Role: "assistant",
				Model: c.model, Content: []any{},
				Usage: models.Usage{InputTokens: 0, OutputTokens: 0},
			},
		})
	}
	if chunk.Usage != nil {
		c.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		events = append(events, c.feedChoice(choice)...)
	}

	return events
}

func (c *StreamConverter) feedChoice(choice struct {
	Index        int                   `json:"index"`
	Delta        models.ChatChunkDelta `json:"delta"`
	FinishReason *string               `json:"finish_reason"`
},
) []models.AnthropicEvent {
	var events []models.AnthropicEvent
	d := choice.Delta

	if d.Reasoning != "" {
		// thinking-блок открывается лениво (при первом reasoning-фрагменте)
		if _, open := c.toolBlocks[-1]; !open {
			c.toolBlocks[-1] = c.nextIndex
			events = append(events, blockStart(c.nextIndex, models.AnthropicBlock{Type: "thinking"}))
			c.nextIndex++
		}
		idx := c.toolBlocks[-1]
		events = append(events, deltaEvent(idx, "thinking_delta", d.Reasoning))
	}
	if d.Content != "" {
		if _, open := c.toolBlocks[-2]; !open {
			c.toolBlocks[-2] = c.nextIndex
			events = append(events, blockStart(c.nextIndex, models.AnthropicBlock{Type: "text", Text: ""}))
			c.nextIndex++
		}
		events = append(events, deltaEvent(c.toolBlocks[-2], "text_delta", d.Content))
	}
	for _, call := range d.ToolCalls {
		anthropicIdx, open := c.toolBlocks[call.Index]
		if !open {
			anthropicIdx = c.nextIndex
			c.toolBlocks[call.Index] = anthropicIdx
			events = append(events, blockStart(anthropicIdx, models.AnthropicBlock{
				Type: "tool_use", ID: call.ID, Name: call.Function.Name,
			}))
			c.nextIndex++
		}
		if call.Function.Arguments != "" {
			events = append(events, deltaEvent(anthropicIdx, "input_json_delta", call.Function.Arguments))
		}
	}
	if choice.FinishReason != nil && *choice.FinishReason != "" {
		c.stopReason = stopReason(*choice.FinishReason)
	}

	return events
}

// Finish закрывает открытые блоки (в порядке открытия — детерминизм) и
// завершает сообщение; message_stop строго без payload.
func (c *StreamConverter) Finish() []models.AnthropicEvent {
	if !c.started {
		return nil
	}
	events := make([]models.AnthropicEvent, 0, len(c.toolBlocks)+2)
	events = append(events, c.closeBlocks()...)
	usage := models.Usage{}
	if c.usage != nil {
		usage = *c.usage
	}
	events = append(events, models.AnthropicEvent{
		Type: evMessageDelta,
		Delta: &models.AnthropicDelta{
			Type:       evMessageDelta,
			StopReason: firstNonEmptyStr(c.stopReason, "end_turn"),
		},
		Usage: &usage,
	})
	events = append(events, models.AnthropicEvent{Type: "message_stop"})

	return events
}

// FinishBroken — финал после обрыва апстрима: блоки закрыты, stop_reason
// не подменяет ошибку (SSE-error уже эмитнут релеем до этого).
func (c *StreamConverter) FinishBroken() []models.AnthropicEvent {
	if !c.started {
		return nil
	}
	events := make([]models.AnthropicEvent, 0, len(c.toolBlocks)+1)
	events = append(events, c.closeBlocks()...)
	events = append(events, models.AnthropicEvent{
		Type:  evMessageDelta,
		Delta: &models.AnthropicDelta{Type: evMessageDelta, StopReason: "max_tokens"},
	})

	return events
}

// closeBlocks — content_block_stop по возрастанию индекса (порядок открытия).
func (c *StreamConverter) closeBlocks() []models.AnthropicEvent {
	indexes := make([]int, 0, len(c.toolBlocks))
	seenIdx := map[int]bool{}
	for _, idx := range c.toolBlocks {
		if !seenIdx[idx] {
			seenIdx[idx] = true
			indexes = append(indexes, idx)
		}
	}
	slices.Sort(indexes)
	events := make([]models.AnthropicEvent, 0, len(indexes))
	for _, idx := range indexes {
		events = append(events, blockStop(idx))
	}

	return events
}

func blockStart(index int, block models.AnthropicBlock) models.AnthropicEvent {
	idx := index

	return models.AnthropicEvent{Type: "content_block_start", Index: &idx, ContentBlock: &block}
}

func blockStop(index int) models.AnthropicEvent {
	idx := index

	return models.AnthropicEvent{Type: "content_block_stop", Index: &idx}
}

func deltaEvent(index int, deltaType, value string) models.AnthropicEvent {
	idx := index
	delta := models.AnthropicDelta{Type: deltaType}
	switch deltaType {
	case "thinking_delta":
		delta.Thinking = value
	case "input_json_delta":
		delta.PartialJSON = value
	default: // text_delta и прочие текстовые
		delta.Text = value
	}

	return models.AnthropicEvent{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &delta,
	}
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
