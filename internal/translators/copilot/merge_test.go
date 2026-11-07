package copilot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// checked test accessors (errcheck): a wrong shape fails the assertion with
// a location instead of panicking.
func msgAt(t *testing.T, messages []any, i int) map[string]any {
	t.Helper()
	msg, ok := messages[i].(map[string]any)
	require.True(t, ok, "message %d is not a map", i)

	return msg
}

func blocksOf(t *testing.T, v any) []any {
	t.Helper()
	blocks, ok := v.([]any)
	require.True(t, ok, "value is not a block slice")

	return blocks
}

func blockAt(t *testing.T, blocks []any, i int) map[string]any {
	t.Helper()
	blk, ok := blocks[i].(map[string]any)
	require.True(t, ok, "block %d is not a map", i)

	return blk
}

func textOf(t *testing.T, blk map[string]any) string {
	t.Helper()
	text, ok := blk["text"].(string)
	require.True(t, ok, "block has no text")

	return text
}

func messagesOf(t *testing.T, m map[string]any) []any {
	t.Helper()
	msgs, ok := m["messages"].([]any)
	require.True(t, ok, "no messages")

	return msgs
}

// Inside a user message that carries tool_results, loose text blocks are
// absorbed into the LAST tool_result's content — the gateway re-bills a
// mixed message as a new user interaction.
func TestMergeToolResults_AbsorbsTextIntoLastResult(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"t1","content":"first"},
		{"type":"text","text":"meanwhile"},
		{"type":"tool_result","tool_use_id":"t2","content":"second"},
		{"type":"text","text":"and this"}
	]}]}`
	out := mergeToolResults(body(t, raw))
	msg := msgAt(t, messagesOf(t, out), 0)
	content := blocksOf(t, msg["content"])
	require.Len(t, content, 2)
	require.Equal(t, "tool_result", blockAt(t, content, 0)["type"])
	second := blockAt(t, content, 1)
	require.Equal(t, "tool_result", second["type"])
	// string content became blocks: the original text + the two absorbed
	blocks := blocksOf(t, second["content"])
	require.Len(t, blocks, 3)
	require.Equal(t, "second", textOf(t, blockAt(t, blocks, 0)))
	require.Equal(t, "meanwhile", textOf(t, blockAt(t, blocks, 1)))
	require.Equal(t, "and this", textOf(t, blockAt(t, blocks, 2)))
}

// Consecutive user messages that carry only tool_results merge into one.
func TestMergeToolResults_MergesConsecutiveToolOnlyMessages(t *testing.T) {
	raw := `{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"a","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"one"}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"two"}]}
	]}`
	out := mergeToolResults(body(t, raw))
	msgs := messagesOf(t, out)
	require.Len(t, msgs, 2)
	merged := msgAt(t, msgs, 1)
	require.Equal(t, "user", merged["role"])
	require.Len(t, blocksOf(t, merged["content"]), 2)
}

// Pure user text and assistant messages are untouched.
func TestMergeToolResults_LeavesPlainText(t *testing.T) {
	raw := `{"messages":[
		{"role":"user","content":"hello"},
		{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"a","input":{}}]}
	]}`
	out := mergeToolResults(body(t, raw))
	require.Equal(t, body(t, raw), out)
}

// A tool_result whose content is already block-shaped absorbs text as extra
// blocks, without converting the existing ones.
func TestMergeToolResults_BlockContentAppends(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"orig"}]},
		{"type":"text","text":"extra"}
	]}]}`
	out := mergeToolResults(body(t, raw))
	content := blocksOf(t, msgAt(t, messagesOf(t, out), 0)["content"])
	require.Len(t, content, 1)
	blocks := blocksOf(t, blockAt(t, content, 0)["content"])
	require.Len(t, blocks, 2)
	require.Equal(t, "extra", textOf(t, blockAt(t, blocks, 1)))
}
