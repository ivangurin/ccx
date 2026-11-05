package openai_test

import (
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

func TestBuildResponse_Text(t *testing.T) {
	resp := &models.ChatResponse{}
	resp.Choices = append(resp.Choices, struct {
		Message      models.ChatMessage `json:"message"`
		FinishReason *string            `json:"finish_reason"`
	}{Message: models.ChatMessage{Role: "assistant", Content: "привет"}})
	stop := "stop"
	resp.Choices[0].FinishReason = &stop
	resp.Usage = models.Usage{InputTokens: 10, OutputTokens: 5}

	out := openai.BuildMessagesResponse(resp, "gpt")

	require.Equal(t, "assistant", out.Role)
	require.Len(t, out.Content, 1)
	require.Equal(t, "text", out.Content[0].Type)
	require.Equal(t, "привет", *out.Content[0].Text)
	require.NotNil(t, out.StopReason)
	require.Equal(t, "end_turn", *out.StopReason)
	require.Equal(t, 10, out.Usage.InputTokens)
}

func TestBuildResponse_ToolCalls(t *testing.T) {
	resp := &models.ChatResponse{}
	resp.Choices = append(resp.Choices, struct {
		Message      models.ChatMessage `json:"message"`
		FinishReason *string            `json:"finish_reason"`
	}{Message: models.ChatMessage{
		Role: "assistant",
		ToolCalls: []models.ChatToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: models.ChatFunction{Name: "bash", Arguments: `{"cmd":"ls"}`},
		}},
	}})
	tcalls := "tool_calls"
	resp.Choices[0].FinishReason = &tcalls

	out := openai.BuildMessagesResponse(resp, "gpt")

	require.Len(t, out.Content, 1)
	require.Equal(t, "tool_use", out.Content[0].Type)
	require.Equal(t, "call_1", *out.Content[0].ID)
	require.Equal(t, "bash", *out.Content[0].Name)
	require.JSONEq(t, `{"cmd":"ls"}`, string(out.Content[0].Input))
	require.Equal(t, "tool_use", *out.StopReason)
}

func TestBuildResponse_Length(t *testing.T) {
	resp := &models.ChatResponse{}
	resp.Choices = append(resp.Choices, struct {
		Message      models.ChatMessage `json:"message"`
		FinishReason *string            `json:"finish_reason"`
	}{Message: models.ChatMessage{Content: "длинный"}})
	length := "length"
	resp.Choices[0].FinishReason = &length

	out := openai.BuildMessagesResponse(resp, "gpt")
	require.Equal(t, "max_tokens", *out.StopReason)
}
