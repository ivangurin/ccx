// Package openai — транслятор Anthropic Messages → OpenAI chat/completions
// (api.openai.com и корпоративные OpenAI-совместимые шлюзы).
package openai

import (
	"encoding/json"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
)

// типы блоков/ролей (goconst).
const (
	blockText     = "text"
	blockToolUse  = "tool_use"
	roleAssistant = "assistant"
)

// BuildChatRequest маппит MessagesRequest в chat/completions. maxTokensField —
// имя поля лимита токенов из профиля (max_tokens | max_completion_tokens).
func BuildChatRequest(req *models.MessagesRequest, maxTokensField string) *models.ChatRequest {
	out := &models.ChatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}
	_ = maxTokensField // имя поля применяется на сериализации (custom MarshalJSON)
	if req.Stream {
		out.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, models.ChatTool{
			Type: "function",
			Function: models.ChatFunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	if len(req.ToolChoice) > 0 {
		out.ToolChoice = req.ToolChoice
	}

	if sys := systemText(req.System); sys != "" {
		out.Messages = append(out.Messages, models.ChatMessage{Role: "system", Content: sys})
	}
	for _, msg := range req.Messages {
		out.Messages = append(out.Messages, convertMessage(msg)...)
	}

	return out
}

// systemText извлекает system (строка или text-блоки).
func systemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == blockText {
			parts = append(parts, b.Text)
		}
	}

	return strings.Join(parts, "\n")
}

// convertMessage разворачивает сообщение в chat-сообщения (user-сообщение с
// tool_result блоками становится несколькими сообщениями).
func convertMessage(msg models.Message) []models.ChatMessage {
	// content: строка
	var plain string
	if err := json.Unmarshal(msg.Content, &plain); err == nil {
		return []models.ChatMessage{{Role: msg.Role, Content: plain}}
	}

	var blocks []models.ContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return []models.ChatMessage{{Role: msg.Role}}
	}

	var out []models.ChatMessage
	var assistantTexts []string
	var assistantTools []models.ChatToolCall
	for _, b := range blocks {
		switch b.Type {
		case blockText:
			if msg.Role == roleAssistant {
				assistantTexts = append(assistantTexts, deref(b.Text))
			} else {
				out = append(out, models.ChatMessage{Role: msg.Role, Content: deref(b.Text)})
			}
		case blockToolUse:
			assistantTools = append(assistantTools, models.ChatToolCall{
				ID:   deref(b.ID),
				Type: "function",
				Function: models.ChatFunction{
					Name:      deref(b.Name),
					Arguments: string(b.Input),
				},
			})
		case "tool_result":
			out = append(out, models.ChatMessage{
				Role:       "tool",
				Content:    toolResultText(b),
				ToolCallID: deref(b.ToolUseID),
			})
		default:
			// image и неизвестные блоки пропускаем — не роняем диалог
			// (image_url маппится на сериализации при появлении мультимодала)
		}
	}
	if msg.Role == roleAssistant {
		assistant := models.ChatMessage{
			Role:      "assistant",
			Content:   strings.Join(assistantTexts, ""),
			ToolCalls: assistantTools,
		}

		return []models.ChatMessage{assistant}
	}

	return out
}

func toolResultText(b models.ContentBlock) string {
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b.Content, &blocks); err != nil {
		return string(b.Content)
	}
	parts := make([]string, 0, len(blocks))
	for _, blk := range blocks {
		if blk.Type == blockText {
			parts = append(parts, blk.Text)
		}
	}

	return strings.Join(parts, "\n")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
