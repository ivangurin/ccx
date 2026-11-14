package copilot_test

import (
	"testing"

	"github.com/ivangurin/ccx/internal/translators/copilot"
	"github.com/stretchr/testify/require"
)

func TestDetectSubagent_MarkerInFirstUserMessage(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"__SUBAGENT_MARKER__ explore the repo"}]}`
	require.True(t, copilot.DetectSubagentForTest(body(t, raw)))
}

func TestDetectSubagent_MarkerInBlocks(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":[{"type":"text","text":"__SUBAGENT_MARKER__ go"}]}]}`
	require.True(t, copilot.DetectSubagentForTest(body(t, raw)))
}

func TestDetectSubagent_MarkerInSystem(t *testing.T) {
	raw := `{"system":"__SUBAGENT_MARKER__ agent brief","messages":[{"role":"user","content":"go"}]}`
	require.True(t, copilot.DetectSubagentForTest(body(t, raw)))
}

func TestDetectSubagent_UserIDMarker(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":"user_x_agent_123_session_y"}}`
	require.True(t, copilot.DetectSubagentForTest(body(t, raw)))
}

func TestDetectSubagent_None(t *testing.T) {
	raw := `{"system":"You are Claude Code.","messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":"user_x_account_y_session_z"}}`
	require.False(t, copilot.DetectSubagentForTest(body(t, raw)))
}
