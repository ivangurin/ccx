package copilot

import "strings"

// Subagent detection: turns spawned by the client's Agent tool run inside an
// already-billed interaction and carry a marker the client itself injects;
// the gateway must not bill them as fresh premium interactions.

const subagentMarker = "__SUBAGENT_MARKER__"

const subagentUserMarker = "_agent_"

// detectSubagent matches two signals: the marker in the brief (the first
// system or user message's text) or the _agent_ segment of metadata.user_id.
func detectSubagent(m map[string]any) bool {
	if strings.Contains(systemText(m), subagentMarker) {
		return true
	}
	if messages := asSlice(m["messages"]); len(messages) > 0 {
		if first := asMap(messages[0]); first != nil && first["role"] == initiatorUser {
			if strings.Contains(messageText(first), subagentMarker) {
				return true
			}
		}
	}
	if meta := asMap(m["metadata"]); meta != nil {
		if strings.Contains(asString(meta["user_id"]), subagentUserMarker) {
			return true
		}
	}

	return false
}
