package codex

import (
	"encoding/json"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/billing"
	"github.com/ivangurin/ccx/internal/translators/content"
)

// BuildResponsesRequest maps Messages → Responses.
func BuildResponsesRequest(req *models.MessagesRequest) *ResponsesRequest {
	// Claude Code ships SessionStart-hook output as messages with role
	// "system" inside messages[]; the ChatGPT backend rejects input items
	// with that role ("System messages are not allowed", verified live), so
	// they merge into instructions — the channel Codex CLI uses for all
	// system-level text — after the top-level system prompt.
	instructions := content.SystemText(req.System)
	var systemMsgs []string
	out := &ResponsesRequest{
		Model:           req.Model,
		MaxOutputTokens: req.MaxTokens,
		Stream:          req.Stream,
	}
	for _, tool := range req.Tools {
		switch {
		case tool.IsWebSearch():
			// the hosted search tool (see websearch.go); bare object only
			out.Tools = append(out.Tools, ResponsesTool{Type: responsesToolWebSearch})
		case tool.IsServerTool():
			// other server tools have no hosted counterpart: a phantom
			// function nobody executes is worse than absence — that is
			// exactly how WebSearch used to return empty results
		default:
			out.Tools = append(out.Tools, ResponsesTool{
				Type: "function", Name: tool.Name, Description: tool.Description,
				Parameters: tool.InputSchema,
			})
		}
	}
	for _, msg := range req.Messages {
		if msg.Role == roleSystem {
			// same wire shape as the top-level system field (string | blocks)
			systemMsgs = append(systemMsgs, content.SystemText(msg.Content))

			continue
		}
		out.Input = append(out.Input, convertMessage(msg)...)
	}
	if len(systemMsgs) > 0 {
		parts := append([]string{instructions}, systemMsgs...)
		instructions = strings.Join(parts, "\n\n")
	}
	// the client's local billing line must not reach the instructions
	out.Instructions = billing.Strip(instructions)

	return out
}

// convertMessage maps one Anthropic message to Responses items: a plain
// string content is one message item; a block array splits into text /
// function_call / function_call_output items (an assistant's text blocks
// merge into one leading message item).
func convertMessage(msg models.Message) []ResponsesItem {
	var plain string
	if err := json.Unmarshal(msg.Content, &plain); err == nil {
		return []ResponsesItem{messageItem(msg.Role, plain)}
	}
	var blocks []models.ContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil
	}

	return convertBlocks(msg.Role, blocks)
}

// messageItem — one message item carrying a plain text (the assistant role
// is normalized to the wire's "assistant").
func messageItem(role, text string) ResponsesItem {
	if role == roleAssistantS {
		role = "assistant"
	}

	return ResponsesItem{
		Type: typeMessage, Role: role,
		Content: json.RawMessage(contentJSON(role, text)),
	}
}

func convertBlocks(role string, blocks []models.ContentBlock) []ResponsesItem {
	var out []ResponsesItem
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case typeText:
			if role == roleAssistantS {
				texts = append(texts, content.Deref(b.Text))
			} else {
				out = append(out, messageItem(role, content.Deref(b.Text)))
			}
		case "tool_use":
			out = append(out, functionCallItem(b))
		case "tool_result":
			out = append(out, functionCallOutputItem(b))
		default:
			// thinking/image and unknown ones are skipped (reasoning is on the Responses side)
		}
	}
	if role == roleAssistantS && (len(texts) > 0 || len(out) == 0) {
		out = append([]ResponsesItem{messageItem("assistant", strings.Join(texts, ""))}, out...)
	}

	return out
}

// functionCallItem — a tool_use block: nil input must not become omitted
// arguments, the backend rejects the item ("Missing required parameter:
// 'input[N].arguments'", verified live 2026-11-08); an argumentless call is
// the empty object on this wire.
func functionCallItem(b models.ContentBlock) ResponsesItem {
	args := string(b.Input)
	if args == "" {
		args = "{}"
	}

	return ResponsesItem{
		Type: "function_call", Name: content.Deref(b.Name),
		Arguments: args, CallID: content.Deref(b.ID),
	}
}

// functionCallOutputItem — a tool_result block: the pointer keeps output on
// the wire even when the result is empty — omission is a hard 400 (see
// ResponsesItem.Output).
func functionCallOutputItem(b models.ContentBlock) ResponsesItem {
	result := content.ToolResultText(b)

	return ResponsesItem{
		Type: "function_call_output", CallID: content.Deref(b.ToolUseID),
		Output: &result,
	}
}

func contentJSON(role, text string) string {
	field := "input_text"
	if role == roleAssistantS {
		field = "output_text"
	}
	data, _ := json.Marshal([]map[string]string{{"type": field, "text": text}}) //nolint:errchkjson // constant structure

	return string(data)
}
