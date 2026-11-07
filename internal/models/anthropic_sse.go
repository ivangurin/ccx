package models

import "encoding/json"

// AnthropicEvent is a typed SSE event of the Anthropic Messages stream.
// One struct for all types: serialization is driven by Type, empty subobjects are omitempty.
type AnthropicEvent struct {
	Type            string                   `json:"type"`
	Message         *AnthropicMessagePayload `json:"message,omitempty"`
	ContentBlock    *AnthropicBlock          `json:"content_block,omitempty"`
	Index           *int                     `json:"index,omitempty"`
	Delta           *AnthropicDelta          `json:"delta,omitempty"`
	MessageDelta    *AnthropicMessageDelta   `json:"-"`
	Usage           *Usage                   `json:"usage,omitempty"`
	Error           *AnthropicError          `json:"error,omitempty"`
	ContentBlockRaw json.RawMessage          `json:"-"`
}

// AnthropicMessagePayload is message_start.message / message_stop.
type AnthropicMessagePayload struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Model      string `json:"model"`
	Content    []any  `json:"content"`
	StopReason any    `json:"stop_reason"`
	Usage      Usage  `json:"usage"`
}

// AnthropicBlock is content_block_start.content_block.
type AnthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"` // web_search_tool_result: paired server_tool_use id
	Content   json.RawMessage `json:"content,omitempty"`     // web_search_tool_result: []search_result
}

// AnthropicDelta is content_block_delta.delta.
type AnthropicDelta struct {
	Type        string `json:"type"` // text_delta | thinking_delta | input_json_delta | signature_delta
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// AnthropicMessageDelta is message_delta (usage + stop_reason).
type AnthropicMessageDelta struct {
	StopReason string `json:"stop_reason"`
	Usage      Usage  `json:"usage"`
}

// AnthropicError is the error event.
type AnthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
