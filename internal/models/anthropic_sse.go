package models

import "encoding/json"

// AnthropicEvent — типизированное SSE-событие Anthropic Messages stream.
// Одна структура на все типы: сериализация по Type, пустые подобъекты — omitempty.
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

// AnthropicMessagePayload — message_start.message / message_stop.
type AnthropicMessagePayload struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Model      string `json:"model"`
	Content    []any  `json:"content"`
	StopReason any    `json:"stop_reason"`
	Usage      Usage  `json:"usage"`
}

// AnthropicBlock — content_block_start.content_block.
type AnthropicBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// AnthropicDelta — content_block_delta.delta.
type AnthropicDelta struct {
	Type        string `json:"type"` // text_delta | thinking_delta | input_json_delta | signature_delta
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// AnthropicMessageDelta — message_delta (usage + stop_reason).
type AnthropicMessageDelta struct {
	StopReason string `json:"stop_reason"`
	Usage      Usage  `json:"usage"`
}

// AnthropicError — error-событие.
type AnthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
