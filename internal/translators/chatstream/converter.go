package chatstream

import (
	"slices"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/strutil"
)

// event names (goconst).
const evMessageDelta = "message_delta"

// stopReason maps finish_reason → stop_reason.
func stopReason(finish string) string {
	switch finish {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	default: // stop and unknown
		return "end_turn"
	}
}

// StreamConverter turns chat/completions SSE chunks into Anthropic Messages
// events. State: the current block (text/thinking/tool_use per index), the
// final stop_reason/usage. A pure converter — golden-testable.
type StreamConverter struct {
	model     string
	started   bool
	nextIndex int
	// open blocks: tool_use by openai-index → anthropic-index
	toolBlocks map[int]int
	usage      *models.Usage
	stopReason string
}

// NewStreamConverter creates a converter for the model.
func NewStreamConverter(model string) *StreamConverter {
	return &StreamConverter{model: model, toolBlocks: map[int]int{}}
}

// Feed accepts a chunk, returns events.
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
		// the thinking block opens lazily (on the first reasoning fragment)
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
			// Anthropic blocks are contiguous: the lazy text/thinking blocks
			// (sentinels −1/−2) stop HERE — leaving them open made a later
			// text delta land below the tool block and reordered the content
			events = c.closeSentinel(events, -1)
			events = c.closeSentinel(events, -2)
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

// closeSentinel stops the open sentinel block (thinking −1 / text −2), if
// any. Tool blocks close them on open (contiguity, see feedChoice); a
// sentinel opened after a tool block is a fresh, correctly ordered block.
func (c *StreamConverter) closeSentinel(events []models.AnthropicEvent, key int) []models.AnthropicEvent {
	if idx, open := c.toolBlocks[key]; open {
		events = append(events, blockStop(idx))
		delete(c.toolBlocks, key)
	}

	return events
}

// finalUsage is the usage the stream carried (zero when none arrived).
func (c *StreamConverter) finalUsage() models.Usage {
	if c.usage != nil {
		return *c.usage
	}

	return models.Usage{}
}

// Finish closes open blocks (in opening order — determinism) and completes the
// message; message_stop strictly without a payload.
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
			StopReason: strutil.FirstNonEmpty(c.stopReason, "end_turn"),
		},
		Usage: &usage,
	})
	events = append(events, models.AnthropicEvent{Type: "message_stop"})

	return events
}

// FinishBroken is the finale after an upstream break: blocks are closed,
// stop_reason does not mask the error (the SSE error has already been emitted
// by the relay before this).
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

// closeBlocks emits content_block_stop in ascending index order (opening order).
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
	default: // text_delta and other textual ones
		delta.Text = value
	}

	return models.AnthropicEvent{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &delta,
	}
}
