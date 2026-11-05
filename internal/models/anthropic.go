package models

import "encoding/json"

// ContentBlock — блок контента Anthropic-сообщения.
type ContentBlock struct {
	Type      string          `json:"type"` // text | thinking | tool_use | tool_result | image
	Text      *string         `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	ID        *string         `json:"id,omitempty"`
	Name      *string         `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID *string         `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // tool_result: string | blocks
	Source    json.RawMessage `json:"source,omitempty"`  // image
}

// Message — сообщение диалога.
type Message struct {
	Role    string          `json:"role"`    // user | assistant
	Content json.RawMessage `json:"content"` // string | []ContentBlock
}

// Tool — определение инструмента.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// MessagesRequest — POST /v1/messages (поля, нужные трансляторам).
type MessagesRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        json.RawMessage `json:"system,omitempty"` // string | blocks
	Messages      []Message       `json:"messages"`
	Stream        bool            `json:"stream"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	Thinking      json.RawMessage `json:"thinking,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

// Usage — токены. UnmarshalJSON принимает и Anthropic-ключи
// (input_tokens/output_tokens), и OpenAI (prompt_tokens/completion_tokens) —
// апстримы chat/completions шлют второй формат.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// UnmarshalJSON — двойной формат ключей (см. комментарий типа).
func (u *Usage) UnmarshalJSON(data []byte) error {
	var both struct {
		InputTokens   int `json:"input_tokens"`
		OutputTokens  int `json:"output_tokens"`
		PromptTokens  int `json:"prompt_tokens"`
		CompleteToken int `json:"completion_tokens"`
	}
	if err := json.Unmarshal(data, &both); err != nil {
		return err
	}
	u.InputTokens = both.InputTokens
	u.OutputTokens = both.OutputTokens
	if u.InputTokens == 0 && both.PromptTokens != 0 {
		u.InputTokens = both.PromptTokens
	}
	if u.OutputTokens == 0 && both.CompleteToken != 0 {
		u.OutputTokens = both.CompleteToken
	}

	return nil
}

// MessagesResponse — нестримовый ответ.
type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   *string        `json:"stop_reason,omitempty"`
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage"`
}
