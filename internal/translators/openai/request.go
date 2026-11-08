// Package openai: translator from Anthropic Messages to OpenAI chat/completions
// (api.openai.com and corporate OpenAI-compatible gateways).
package openai

import (
	"encoding/json"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/billing"
	"github.com/ivangurin/ccx/internal/translators/content"
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
	sanitizeOrphans(req)
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

	if sys := billing.Strip(content.SystemText(req.System)); sys != "" {
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
	var userTexts []string
	for _, b := range blocks {
		switch b.Type {
		case blockText:
			if msg.Role == roleAssistant {
				assistantTexts = append(assistantTexts, content.Deref(b.Text))
			} else {
				// buffered, not emitted in place: a text block sitting before
				// the message's tool_results (hook/skill text prepended) must
				// not land between the assistant tool_calls and the tool
				// answers — strict upstreams reject a role:tool message that
				// does not directly follow the tool_calls with a 400
				userTexts = append(userTexts, content.Deref(b.Text))
			}
		case blockToolUse:
			assistantTools = append(assistantTools, models.ChatToolCall{
				ID:   content.Deref(b.ID),
				Type: "function",
				Function: models.ChatFunction{
					Name:      content.Deref(b.Name),
					Arguments: string(b.Input),
				},
			})
		case "tool_result":
			out = append(out, models.ChatMessage{
				Role:       "tool",
				Content:    content.ToolResultText(b),
				ToolCallID: content.Deref(b.ToolUseID),
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
	for _, text := range userTexts {
		out = append(out, models.ChatMessage{Role: msg.Role, Content: text})
	}

	return out
}
