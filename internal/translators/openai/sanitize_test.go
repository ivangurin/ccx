package openai_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

// test role constants (goconst): shared by this package's test files.
const (
	tRoleUser      = "user"
	tRoleAssistant = "assistant"
)

func chatBody(msgs ...models.Message) *models.MessagesRequest {
	return &models.MessagesRequest{Model: "gpt", MaxTokens: 10, Messages: msgs}
}

// A tool_result whose call is gone (compacted/truncated history) must arrive
// upstream as a user text message — a tool message with a dangling
// tool_call_id is a guaranteed 400 on strict upstreams.
func TestOrphanToolResultBecomesUserText(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleUser, Content: json.RawMessage(`"hi"`)},
		models.Message{Role: tRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"t1","name":"bash","input":{"cmd":"ls"}}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t_gone","content":"stale output"}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	// system absent; [user hi, assistant+tool_call, user "stale output"]
	require.Len(t, chat.Messages, 3)
	last := chat.Messages[2]
	require.Equal(t, tRoleUser, last.Role)
	require.Equal(t, "stale output", last.Content)
	require.Empty(t, last.ToolCallID)
}

// A paired tool_result stays a tool message keyed by its call id.
func TestPairedToolResultStaysToolMessage(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"t1","name":"bash","input":{}}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	require.Len(t, chat.Messages, 2)
	require.Equal(t, "tool", chat.Messages[1].Role)
	require.Equal(t, "t1", chat.Messages[1].ToolCallID)
}

// Parallel calls: pairing is by id set, not order — both results stay.
func TestParallelToolResultsPairByIDSet(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"t2","name":"a","input":{}},{"type":"tool_use","id":"t1","name":"b","input":{}}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t1","content":"one"},{"type":"tool_result","tool_use_id":"t2","content":"two"}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	require.Len(t, chat.Messages, 3)
	require.Equal(t, "tool", chat.Messages[1].Role)
	require.Equal(t, "t1", chat.Messages[1].ToolCallID)
	require.Equal(t, "tool", chat.Messages[2].Role)
	require.Equal(t, "t2", chat.Messages[2].ToolCallID)
}

// A user message that only carried an empty orphan result disappears rather
// than leaving an empty user turn.
func TestEmptyOrphanDropped(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"t1","name":"bash","input":{}}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t_gone","content":""}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	require.Len(t, chat.Messages, 1)
}

// No preceding assistant message at all → every tool_result is an orphan.
func TestToolResultWithoutAnyAssistantIsOrphan(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t1","content":"resumed session"}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	require.Len(t, chat.Messages, 1)
	require.Equal(t, tRoleUser, chat.Messages[0].Role)
	require.Equal(t, "resumed session", chat.Messages[0].Content)
}

// Parallel results split across consecutive user messages (a real client
// shape) both stay tool messages — the pairing survives until a message
// that is not a tool result arrives.
func TestSplitParallelToolResultsStayToolMessages(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"t1","name":"a","input":{}},{"type":"tool_use","id":"t2","name":"b","input":{}}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t1","content":"one"}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t2","content":"two"}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	require.Len(t, chat.Messages, 3)
	require.Equal(t, "tool", chat.Messages[1].Role)
	require.Equal(t, "t1", chat.Messages[1].ToolCallID)
	require.Equal(t, "tool", chat.Messages[2].Role)
	require.Equal(t, "t2", chat.Messages[2].ToolCallID)
}

// A system hook inserted between the assistant's calls and the results does
// not break the pairing either.
func TestSystemBetweenCallsAndResultsKeepsPairing(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"t1","name":"a","input":{}}]`)},
		models.Message{Role: "system", Content: json.RawMessage(`"hook-context"`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t1","content":"one"}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	// [assistant+call, system hook, tool result] — the hook rides along,
	// the result stays a tool message
	require.Len(t, chat.Messages, 3)
	require.Equal(t, "system", chat.Messages[1].Role)
	require.Equal(t, "tool", chat.Messages[2].Role)
	require.Equal(t, "t1", chat.Messages[2].ToolCallID)
}

// A mixed message (tool_result + hook text) answers SOME calls but does not
// end the turn — a following split result still pairs with its call; the
// answered ids no longer pair (a duplicate of an answered id IS an orphan).
func TestMixedMessageKeepsPendingForSplitResults(t *testing.T) {
	req := chatBody(
		models.Message{Role: tRoleAssistant, Content: json.RawMessage(`[{"type":"tool_use","id":"t1","name":"a","input":{}},{"type":"tool_use","id":"t2","name":"b","input":{}}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t1","content":"one"},{"type":"text","text":"hook output"}]`)},
		models.Message{Role: tRoleUser, Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t2","content":"two"}]`)},
	)
	chat := openai.BuildChatRequest(req, "max_tokens")

	// [assistant, user text (hook), tool one, tool two] — every call answered
	tools := 0
	for _, m := range chat.Messages {
		if m.Role == "tool" {
			tools++
		}
	}
	require.Equal(t, 2, tools, "both parallel calls stay answered")
}
