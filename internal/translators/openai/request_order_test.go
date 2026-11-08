package openai_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// A user message carrying text BEFORE its tool_results (hook/skill text
// prepended) must convert to tool messages directly following the assistant
// tool_calls, with the user text AFTER them: strict chat/completions
// upstreams (OpenAI itself) reject a role:tool message that does not
// directly follow the assistant message carrying the tool_calls.
func TestBuildChatRequest_TextBeforeToolResultsKeepsToolAdjacency(t *testing.T) {
	req := &models.MessagesRequest{
		Model: "m",
		Messages: []models.Message{
			{Role: "assistant", Content: raw(`[
				{"type":"text","text":"calling"},
				{"type":"tool_use","id":"call_1","name":"Read","input":{"path":"x"}}
			]`)},
			{Role: tRoleUser, Content: raw(`[
				{"type":"text","text":"hook note"},
				{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"file body"}]}
			]`)},
		},
	}

	out := openai.BuildChatRequest(req, "")

	require.Len(t, out.Messages, 3)
	require.Equal(t, "assistant", out.Messages[0].Role)
	require.Len(t, out.Messages[0].ToolCalls, 1)
	require.Equal(t, "tool", out.Messages[1].Role, "the tool answer must directly follow the assistant tool_calls")
	require.Equal(t, "call_1", out.Messages[1].ToolCallID)
	require.Equal(t, "user", out.Messages[2].Role)
	require.Equal(t, "hook note", out.Messages[2].Content)
}

// The plain text-only shape is unchanged: each text block stays its own
// user message, in block order.
func TestBuildChatRequest_TextOnlyUserMessageUnchanged(t *testing.T) {
	req := &models.MessagesRequest{
		Model: "m",
		Messages: []models.Message{
			{Role: tRoleUser, Content: raw(`[
				{"type":"text","text":"one"},
				{"type":"text","text":"two"}
			]`)},
		},
	}

	out := openai.BuildChatRequest(req, "")

	require.Len(t, out.Messages, 2)
	require.Equal(t, "one", out.Messages[0].Content)
	require.Equal(t, "two", out.Messages[1].Content)
}
