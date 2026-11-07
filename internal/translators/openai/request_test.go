package openai_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

func req(t *testing.T, raw string) *models.MessagesRequest {
	t.Helper()
	var r models.MessagesRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &r))

	return &r
}

func TestBuild_SystemString(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "gpt", "max_tokens": 100,
		"system": "you are an assistant",
		"messages": [{"role": "user", "content": "hi"}]
	}`), "max_tokens")

	require.Equal(t, "gpt", out.Model)
	require.Equal(t, []models.ChatMessage{
		{Role: "system", Content: "you are an assistant"},
		{Role: "user", Content: "hi"},
	}, out.Messages)
	require.Equal(t, 100, out.MaxTokens)
}

func TestBuild_SystemBlocks(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "gpt", "max_tokens": 1,
		"system": [{"type": "text", "text": "part 1"}, {"type": "text", "text": "part 2"}],
		"messages": [{"role": "user", "content": "hi"}]
	}`), "max_tokens")

	require.Equal(t, "part 1\npart 2", out.Messages[0].Content)
	require.Equal(t, "system", out.Messages[0].Role)
}

// Server-side tools (web_search_*, computer_*, …) have no chat/completions
// counterpart. They used to be mangled into phantom functions nobody
// executes — Claude Code's WebSearch silently returned empty results; now
// they are dropped from the upstream request.
func TestBuild_SkipsServerTools(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "gpt", "max_tokens": 1,
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [
			{"type":"web_search_20250305","name":"web_search"},
			{"name":"bash","description":"run","input_schema":{"type":"object"}}
		]
	}`), "max_tokens")

	require.Len(t, out.Tools, 1)
	require.Equal(t, "bash", out.Tools[0].Function.Name)
}

func TestBuild_ToolUseAndResult(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "gpt", "max_tokens": 1,
		"messages": [
			{"role": "user", "content": "run the tool"},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "tu1", "name": "bash", "input": {"cmd": "ls"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "tu1", "content": "file1\nfile2"}
			]}
		]
	}`), "max_tokens")

	require.Len(t, out.Messages, 3)
	assistant := out.Messages[1]
	require.Equal(t, "assistant", assistant.Role)
	require.Len(t, assistant.ToolCalls, 1)
	require.Equal(t, "tu1", assistant.ToolCalls[0].ID)
	require.Equal(t, "bash", assistant.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"cmd":"ls"}`, assistant.ToolCalls[0].Function.Arguments)

	toolMsg := out.Messages[2]
	require.Equal(t, "tool", toolMsg.Role)
	require.Equal(t, "tu1", toolMsg.ToolCallID)
	require.Equal(t, "file1\nfile2", toolMsg.Content)
}

func TestBuild_ToolResultBlockContent(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "gpt", "max_tokens": 1,
		"messages": [
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "t1", "name": "bash", "input": {}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "t1", "content": [{"type": "text", "text": "res"}]}
			]}
		]
	}`), "max_tokens")

	// the paired tool_result → a single tool message
	require.Len(t, out.Messages, 2)
	require.Equal(t, "res", out.Messages[1].Content)
	require.Equal(t, "t1", out.Messages[1].ToolCallID)
}

func TestBuild_InlineSystemMessages(t *testing.T) {
	// Claude Code inserts system hooks AFTER user — order is preserved
	out := openai.BuildChatRequest(req(t, `{
		"model": "gpt", "max_tokens": 1,
		"messages": [
			{"role": "user", "content": "question"},
			{"role": "system", "content": "hook-context"}
		]
	}`), "max_tokens")

	require.Equal(t, []models.ChatMessage{
		{Role: "user", Content: "question"},
		{Role: "system", Content: "hook-context"},
	}, out.Messages)
}

func TestBuild_ToolsMapping(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "gpt", "max_tokens": 1,
		"messages": [{"role": "user", "content": "x"}],
		"tools": [{"name": "bash", "description": "shell", "input_schema": {"type": "object"}}],
		"tool_choice": {"type": "auto"}
	}`), "max_tokens")

	require.Len(t, out.Tools, 1)
	require.Equal(t, "bash", out.Tools[0].Function.Name)
	require.Equal(t, "shell", out.Tools[0].Function.Description)
	require.JSONEq(t, `{"type":"object"}`, string(out.Tools[0].Function.Parameters))
	require.JSONEq(t, `"auto"`, string(out.ToolChoice))
}

// Anthropic tool_choice shapes are invalid for chat/completions: passthrough
// earned a 400 on every forced-tool-use turn. any → "required", a named tool
// → the forced-function object, auto → the string form, unknown → dropped.
func TestBuild_ToolChoice_MapsAnthropicShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"any", `{"type": "any"}`, `"required"`},
		{"tool", `{"type": "tool", "name": "bash"}`, `{"type":"function","function":{"name":"bash"}}`},
		{"auto", `{"type": "auto"}`, `"auto"`},
		{"unknown dropped", `{"type": "weird"}`, ``},
	}
	for _, tc := range cases { //nolint:paralleltest // table shares nothing
		t.Run(tc.name, func(t *testing.T) {
			out := openai.BuildChatRequest(req(t, `{
				"model": "gpt", "max_tokens": 1,
				"messages": [{"role": "user", "content": "x"}],
				"tool_choice": `+tc.raw+`
			}`), "max_tokens")
			if tc.want == "" {
				require.Empty(t, string(out.ToolChoice))

				return
			}
			require.JSONEq(t, tc.want, string(out.ToolChoice))
		})
	}
}

func TestBuild_MaxTokensField(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{"model": "m", "max_tokens": 77, "messages": []}`), "max_completion_tokens")
	require.Equal(t, 77, out.MaxTokens)
	require.Empty(t, out.Tools)
}

func TestBuild_StreamSetsIncludeUsage(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{"model": "m", "max_tokens": 1, "stream": true, "messages": []}`), "max_tokens")
	require.True(t, out.Stream)
	require.NotNil(t, out.StreamOptions)
	require.True(t, out.StreamOptions.IncludeUsage)
}

func TestBuild_SamplingParams(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "m", "max_tokens": 1,
		"temperature": 0.5, "top_p": 0.9, "stop_sequences": ["END"],
		"messages": [{"role": "user", "content": "x"}]
	}`), "max_tokens")
	require.NotNil(t, out.Temperature)
	require.InDelta(t, 0.5, *out.Temperature, 1e-9)
	require.Equal(t, []string{"END"}, out.Stop)
}

func TestBuild_AssistantThinkingDropped(t *testing.T) {
	out := openai.BuildChatRequest(req(t, `{
		"model": "m", "max_tokens": 1,
		"messages": [
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "deliberations"},
				{"type": "text", "text": "the answer"}
			]}
		]
	}`), "max_tokens")
	require.Equal(t, "the answer", out.Messages[0].Content)
	require.Empty(t, out.Messages[0].ToolCalls)
}
