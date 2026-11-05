package models

import "encoding/json"

// ChatMessage is a chat/completions message.
type ChatMessage struct {
	Role       string         `json:"role"` // system | user | assistant | tool
	Content    string         `json:"content"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// ChatToolCall is a tool call in an assistant response.
type ChatToolCall struct {
	Index    int          `json:"index,omitempty"` // stream fragmentation
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"` // function
	Function ChatFunction `json:"function"`
}

// ChatFunction is a call's name + arguments (arguments is a JSON string!).
type ChatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatTool is a tool definition.
type ChatTool struct {
	Type     string          `json:"type"` // function
	Function ChatFunctionDef `json:"function"`
}

// ChatFunctionDef is a function definition.
type ChatFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ChatRequest is POST /v1/chat/completions.
type ChatRequest struct {
	Model         string        `json:"model"`
	Messages      []ChatMessage `json:"messages"`
	MaxTokens     int           `json:"max_tokens,omitempty"` // the field named by max_tokens_field
	Stream        bool          `json:"stream,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Tools       []ChatTool      `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	User        string          `json:"-"`
}

// ChatChunkDelta is a stream delta.
type ChatChunkDelta struct {
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	Reasoning string         `json:"reasoning_content,omitempty"`
	ToolCalls []ChatToolCall `json:"tool_calls,omitempty"` // fragmented: id/name or arguments
}

// ChatChunk is an SSE chunk of chat/completions.
type ChatChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index        int            `json:"index"`
		Delta        ChatChunkDelta `json:"delta"`
		FinishReason *string        `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// ChatResponse is a non-streaming response.
type ChatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message      ChatMessage `json:"message"`
		FinishReason *string     `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}
