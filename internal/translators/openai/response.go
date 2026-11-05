package openai

import (
	"github.com/ivangurin/ccx/internal/models"
)

// stopReasonMap maps finish_reason → stop_reason.
var stopReasonMap = map[string]string{
	"stop":           "end_turn",
	"tool_calls":     blockToolUse,
	"length":         "max_tokens",
	"content_filter": "refusal",
}

// BuildMessagesResponse converts a non-stream response into a MessagesResponse.
func BuildMessagesResponse(resp *models.ChatResponse, model string) *models.MessagesResponse {
	out := &models.MessagesResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: model,
		Usage: resp.Usage,
	}
	if len(resp.Choices) == 0 {
		return out
	}
	choice := resp.Choices[0]

	if choice.Message.Content != "" {
		text := choice.Message.Content
		out.Content = append(out.Content, models.ContentBlock{Type: blockText, Text: &text})
	}
	for _, call := range choice.Message.ToolCalls {
		id, name := call.ID, call.Function.Name
		out.Content = append(out.Content, models.ContentBlock{
			Type:  blockToolUse,
			ID:    &id,
			Name:  &name,
			Input: []byte(call.Function.Arguments),
		})
	}

	if choice.FinishReason != nil {
		reason := stopReason(*choice.FinishReason)
		out.StopReason = &reason
	}

	return out
}

func stopReason(finish string) string {
	if r, ok := stopReasonMap[finish]; ok {
		return r
	}

	return "end_turn"
}
