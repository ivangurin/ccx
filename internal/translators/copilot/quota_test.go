package copilot

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

// The gateway bills a premium interaction per user-initiated turn: a tool
// continuation is the SAME interaction carrying its results, it must be
// marked agent or every tool turn re-bills.
func TestClassifyQuota(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"tool continuation is agent", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"ok"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"out"}]}]}`, initiatorAgent},
		{"mixed tool_result+text is agent", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"out"},{"type":"text","text":"and this"}]}]}`, initiatorAgent},
		{"plain text user is user", `{"messages":[{"role":"user","content":"hello there"}]}`, initiatorUser},
		{"string content user is user", `{"messages":[{"role":"user","content":"hello"}]}`, initiatorUser},
		{"no messages is user", `{}`, initiatorUser},
		{"last message assistant is user", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"thinking"}]}]}`, initiatorUser},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyQuota(body(t, tc.raw)).Initiator)
		})
	}
}

// A subagent turn is agent-initiated even when its first message is user text.
func TestClassifyQuota_SubagentOverridesToAgent(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"__SUBAGENT_MARKER__ do the side quest"}],"metadata":{"user_id":"user_x_account_y_session_z"}}`
	cls := classifyQuota(body(t, raw))
	require.Equal(t, initiatorAgent, cls.Initiator)
	require.True(t, cls.IsSubagent)
}

// A compact request is agent-initiated (the client compacting its own
// context is not a new user interaction).
func TestClassifyQuota_CompactIsAgent(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."}]}`
	cls := classifyQuota(body(t, raw))
	require.Equal(t, initiatorAgent, cls.Initiator)
	require.True(t, cls.IsCompact)
}

// A truncation continuation (the prefill fix repacks the trailing assistant
// into a user message led by the machine instruction) is a continuation of
// the billed turn, not a new interaction — the classifier must know the
// marker.
func TestClassifyQuota_PrefillContinuationIsAgent(t *testing.T) {
	payload := prefill.Instruction + "…the cut text…"
	raw := `{"messages":[{"role":"user","content":"go on"}]}`
	m := body(t, raw)
	msgs, ok := m["messages"].([]any)
	require.True(t, ok)
	msg, ok := msgs[0].(map[string]any)
	require.True(t, ok)
	msg["content"] = payload
	require.Equal(t, initiatorAgent, classifyQuota(m).Initiator)
}
