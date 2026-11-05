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

// asString — type-assert с фейлом теста (errcheck: проверять обязателен).
func asString(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	require.True(t, ok, "ожидалась строка, получено %T", v)

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
		{"role":"user","content":"напиши story"},
		{"role":"assistant","content":[{"type":"text","text":"Жил-был"}]}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "prefill:text")
	m := msgs(t, out)
	require.Equal(t, "user", m[len(m)-1]["role"])
	content := asString(t, m[len(m)-1]["content"])
	require.Contains(t, content, "[continuation]")
	require.Contains(t, content, "Жил-был")
	// assistant-prefill удалён
	for _, msg := range m {
		require.NotEqual(t, "assistant", msg["role"])
	}
}

func TestRewrite_AssistantStringPrefill(t *testing.T) {
	in := body(t, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"начало"}]}`)
	out, note := prefill.Rewrite(in)
	require.Contains(t, note, "prefill:text")
	m := msgs(t, out)
	require.Contains(t, asString(t, m[len(m)-1]["content"]), "начало")
}

func TestRewrite_ThinkingOnlyPrefill(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":"x"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"мысли"}]}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "prefill:thinking")
	m := msgs(t, out)
	content := asString(t, m[len(m)-1]["content"])
	// thinking не переносится — заглушка «без текста»
	require.Contains(t, content, "оборвался без текста")
	require.NotContains(t, content, "мысли")
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
	// Claude Code: system-хук-контекст ПОСЛЕ префилла — хвост переносится перед user
	in := body(t, `{"messages":[
		{"role":"user","content":"x"},
		{"role":"assistant","content":[{"type":"text","text":"полу"}]},
		{"role":"system","content":"хук-контекст"}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "sys-tail:1")
	m := msgs(t, out)
	require.Equal(t, "user", m[len(m)-1]["role"])   // финал — user
	require.Equal(t, "system", m[len(m)-2]["role"]) // system-хвост ПЕРЕД user
	require.Contains(t, asString(t, m[len(m)-1]["content"]), "полу")
}

func TestRewrite_UserFinalWithSystemTail_Merged(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":"вопрос"},
		{"role":"system","content":"хук-инфо"}
	]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "sys-tail-merged:1")
	m := msgs(t, out)
	require.Len(t, m, 1) // только user
	content := asString(t, m[0]["content"])
	require.Contains(t, content, "вопрос")
	require.Contains(t, content, "[system context]")
	require.Contains(t, content, "хук-инфо")
}

func TestRewrite_UserBlocksWithSystemTail_Merged(t *testing.T) {
	in := body(t, `{"messages":[
		{"role":"user","content":[{"type":"text","text":"вопрос"}]},
		{"role":"system","content":"хук"}
	]}`)
	out, _ := prefill.Rewrite(in)

	m := msgs(t, out)
	require.Len(t, m, 1)
	blocks, ok := m[0]["content"].([]any)
	require.True(t, ok, "ожидались блоки")
	require.Len(t, blocks, 2) // исходный блок + system-блок
	lastBlock, lok := blocks[1].(map[string]any)
	require.True(t, lok)
	require.Contains(t, asString(t, lastBlock["text"]), "[system context]")
}

func TestRewrite_SystemOnlyMessages(t *testing.T) {
	in := body(t, `{"messages":[{"role":"system","content":"только системное"}]}`)
	out, note := prefill.Rewrite(in)

	require.Contains(t, note, "sys-only:1")
	m := msgs(t, out)
	require.Len(t, m, 1)
	require.Equal(t, "user", m[0]["role"])
	require.Contains(t, asString(t, m[0]["content"]), "только системное")
}

func TestRewrite_InstructionExact(t *testing.T) {
	in := body(t, `{"messages":[{"role":"assistant","content":[{"type":"text","text":"прод"}]}]}`)
	out, _ := prefill.Rewrite(in)
	m := msgs(t, out)
	require.Contains(t, asString(t, m[len(m)-1]["content"]),
		"[continuation] Предыдущий ответ оборвался на тексте ниже. Продолжи его точно с места обрыва: не повторяй написанное, не комментируй, сразу продолжение:")
}

func TestRewrite_DoesNotMutateInput(t *testing.T) {
	in := body(t, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"пре"}]}`)
	snapshot := body(t, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"пре"}]}`)

	_, _ = prefill.Rewrite(in)
	require.Equal(t, snapshot, in)
}
