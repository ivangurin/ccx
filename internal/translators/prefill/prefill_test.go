package prefill_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccx/internal/translators/prefill"
	"github.com/stretchr/testify/require"
)

func body(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &m))

	return m
}

// asString is a type-assert with a test failure (errcheck: checking is mandatory).
func asString(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	require.True(t, ok, "expected a string, got %T", v)

	return s
}

func msgs(t *testing.T, b map[string]any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(b["messages"])
	require.NoError(t, err)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

func TestRewrite_NoMessages_Noop(t *testing.T) {
	in := body(t, `{"model":"m","messages":[]}`)
	out, note := prefill.Rewrite(in)
	require.Equal(t, "no-messages", note)
	require.Equal(t, in, out)
}

func TestRewrite_UserFinal_Noop(t *testing.T) {
	in := body(t, `{"messages":[{"role":"user","content":"hi"}]}`)
	out, note := prefill.Rewrite(in)
	require.Equal(t, "no-prefill", note)
	require.Equal(t, in, out)
}

func TestRewrite_AssistantTextPrefill(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":"write a story"},
		{"role":"assistant","content":[{"type":"text","text":"Once upon"}]}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "prefill:text")
	m := msgs(t, out)
	require.Equal(t, "user", m[len(m)-1]["role"])
	content := asString(t, m[len(m)-1]["content"])
	require.Contains(t, content, "[continuation]")
	require.Contains(t, content, "Once upon")
	// the assistant prefill is removed
	for _, msg := range m {
		require.NotEqual(t, "assistant", msg["role"])
	}
}

func TestRewrite_AssistantStringPrefill(t *testing.T) {
	in := body(t, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"the beginning"}]}`)
	out, note := prefill.Rewrite(in)
	require.Contains(t, note, "prefill:text")
	m := msgs(t, out)
	require.Contains(t, asString(t, m[len(m)-1]["content"]), "the beginning")
}

func TestRewrite_ThinkingOnlyPrefill(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":"x"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"thoughts"}]}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "prefill:thinking")
	m := msgs(t, out)
	content := asString(t, m[len(m)-1]["content"])
	// thinking is not carried over — the "without text" stub
	require.Contains(t, content, "cut off without text")
	require.NotContains(t, content, "thoughts")
}

func TestRewrite_EmptyAssistantPrefill(t *testing.T) {
	in := body(t, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":[]}]}`)
	out, note := prefill.Rewrite(in)
	require.Contains(t, note, "prefill:empty")
	m := msgs(t, out)
	require.Equal(t, "user", m[len(m)-1]["role"])
}

func TestRewrite_ToolUsePrefill(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":"x"},
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash","input":{}}]}
	]}`)
	out, note := prefill.Rewrite(in)
	require.Contains(t, note, "prefill:tool_use")
	m := msgs(t, out)
	require.Equal(t, "user", m[len(m)-1]["role"])
}

func TestRewrite_AssistantBeforeSystemTail(t *testing.T) {
	// Claude Code: system hook context AFTER the prefill — the tail is moved before the user
	in := body(t, `{"messages":[
		{"role":"user","content":"x"},
		{"role":"assistant","content":[{"type":"text","text":"hal"}]},
		{"role":"system","content":"hook-context"}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "sys-tail:1")
	m := msgs(t, out)
	require.Equal(t, "user", m[len(m)-1]["role"])   // the finale is user
	require.Equal(t, "system", m[len(m)-2]["role"]) // the system tail BEFORE the user
	require.Contains(t, asString(t, m[len(m)-1]["content"]), "hal")
}

func TestRewrite_UserFinalWithSystemTail_Merged(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":"question"},
		{"role":"system","content":"hook-info"}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "sys-tail-merged:1")
	m := msgs(t, out)
	require.Len(t, m, 1) // user only
	content := asString(t, m[0]["content"])
	require.Contains(t, content, "question")
	require.Contains(t, content, "[system context]")
	require.Contains(t, content, "hook-info")
}

func TestRewrite_UserBlocksWithSystemTail_Merged(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":[{"type":"text","text":"question"}]},
		{"role":"system","content":"hook"}
	]}`)
	out, _ := prefill.Rewrite(in)

	m := msgs(t, out)
	require.Len(t, m, 1)
	blocks, ok := m[0]["content"].([]any)
	require.True(t, ok, "blocks expected")
	require.Len(t, blocks, 2) // the original block + the system block
	lastBlock, lok := blocks[1].(map[string]any)
	require.True(t, lok)
	require.Contains(t, asString(t, lastBlock["text"]), "[system context]")
}

func TestRewrite_SystemOnlyMessages(t *testing.T) {
	in := body(t, `{"messages":[{"role":"system","content":"system only"}]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "sys-only:1")
	m := msgs(t, out)
	require.Len(t, m, 1)
	require.Equal(t, "user", m[0]["role"])
	require.Contains(t, asString(t, m[0]["content"]), "system only")
}

func TestRewrite_InstructionExact(t *testing.T) {
	in := body(t, `{"messages":[{"role":"assistant","content":[{"type":"text","text":"cont"}]}]}`)
	out, _ := prefill.Rewrite(in)
	m := msgs(t, out)
	require.Contains(t, asString(t, m[len(m)-1]["content"]),
		"[continuation] The previous response was cut off at the text below. Continue it exactly from the point of the cut: do not repeat what was written, do not comment, continue immediately:")
}

func TestRewrite_DoesNotMutateInput(t *testing.T) {
	in := body(t, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"pre"}]}`)
	snapshot := body(t, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"pre"}]}`)

	_, _ = prefill.Rewrite(in)
	require.Equal(t, snapshot, in)
}
