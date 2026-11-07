// Package openai: translator from Anthropic Messages to OpenAI chat/completions
// (api.openai.com and corporate OpenAI-compatible gateways).
package openai

import (
	"encoding/json"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/billing"
)

// block/role types (goconst).
const (
	blockText     = "text"
	blockToolUse  = "tool_use"
	roleAssistant = "assistant"
)

// BuildChatRequest maps a MessagesRequest to chat/completions. maxTokensField
// is the token-limit field name from the profile (max_tokens | max_completion_tokens).
func BuildChatRequest(req *models.MessagesRequest, maxTokensField string) *models.ChatRequest {
	out := &models.ChatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}
	_ = maxTokensField // the field name is applied at serialization (custom MarshalJSON)
	if req.Stream {
		out.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	for _, tool := range req.Tools {
		if tool.IsServerTool() {
			// no chat/completions counterpart: a phantom function nobody
			// executes is worse than absence — WebSearch used to come back
			// empty through exactly this mangling
			continue
		}
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
		out.ToolChoice = mapToolChoice(req.ToolChoice)
	}

	if sys := billing.Strip(systemText(req.System)); sys != "" {
		out.Messages = append(out.Messages, models.ChatMessage{Role: "system", Content: sys})
	}
	for _, msg := range req.Messages {
		out.Messages = append(out.Messages, convertMessage(msg)...)
	}

	return out
}

// mapToolChoice translates Anthropic tool_choice into the chat/completions
// shape: the Anthropic forms are invalid upstream ("any", a named tool) and
// passthrough failed every forced-tool-use turn with a 400. Unknown/empty
// shapes are dropped — no tool_choice beats an invalid one.
func mapToolChoice(raw json.RawMessage) json.RawMessage {
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil || tc.Type == "" {
		return nil
	}
	switch tc.Type {
	case "auto":
		return json.RawMessage(`"auto"`)
	case "any": // "some tool, any tool" → OpenAI's required
		return json.RawMessage(`"required"`)
	case "tool":
		if tc.Name == "" {
			return nil
		}
		name, err := json.Marshal(tc.Name)
		if err != nil {
			return nil
		}

		return json.RawMessage(`{"type":"function","function":{"name":` + string(name) + `}}`)
	default:
		return nil
	}
}

// systemText extracts the system (a string or text blocks).
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

// convertMessage expands a message into chat messages (a user message with
// tool_result blocks becomes several messages).
func convertMessage(msg models.Message) []models.ChatMessage {
	// content: a string
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
			// image and unknown blocks are skipped — don't drop the dialogue
			// (image_url is mapped at serialization once multimodal arrives)
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
