package copilot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Signal 1: the compact system prompt prefix (client-generated, the user
// cannot set the system prompt).
func TestIsCompactRequest_SystemPrefix(t *testing.T) {
	raw := `{"system":"You are a helpful AI assistant tasked with summarizing conversations. Be concise.","messages":[{"role":"user","content":"summarize"}]}`
	require.True(t, isCompactRequest(body(t, raw)))
}

func TestIsCompactRequest_SystemBlocks(t *testing.T) {
	raw := `{"system":[{"type":"text","text":"You are a helpful AI assistant tasked with summarizing conversations"}],"messages":[{"role":"user","content":"x"}]}`
	require.True(t, isCompactRequest(body(t, raw)))
}

// Signal 2: the machine instruction (exact, case-sensitive).
func TestIsCompactRequest_MachineInstruction(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"Pending junk\nCRITICAL: Respond with TEXT ONLY. Do NOT call any tools."}]}`
	require.True(t, isCompactRequest(body(t, raw)))
}

// Signal 3: the two structural markers together.
func TestIsCompactRequest_StructuralMarkers(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"## Pending Tasks:\n- a\n## Current Work:\n- b"}]}`
	require.True(t, isCompactRequest(body(t, raw)))
}

// Anti-false-positive: a user typing one of the markers alone is NOT compact.
func TestIsCompactRequest_UserTextIsNotCompact(t *testing.T) {
	require.False(t, isCompactRequest(body(t, `{"messages":[{"role":"user","content":"Pending Tasks: fix the login bug"}]}`)))
	require.False(t, isCompactRequest(body(t, `{"messages":[{"role":"user","content":"Current Work: the api"}]}`)))
	require.False(t, isCompactRequest(body(t, `{"messages":[{"role":"user","content":"critical: respond with text only. do not call any tools."}]}`)))
	require.False(t, isCompactRequest(body(t, `{"messages":[{"role":"user","content":"just chatting"}]}`)))
}

// The signals are checked on the LAST user message only.
func TestIsCompactRequest_LastUserMessageOnly(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."},{"role":"assistant","content":"ok"},{"role":"user","content":"normal follow-up"}]}`
	require.False(t, isCompactRequest(body(t, raw)))
}
