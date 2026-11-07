package copilot

import "strings"

// Compact detection: the client compacting its own context is not a new user
// interaction, and compact requests must be marked agent-initiated. Only
// machine-generated signals count — matching generic phrases a user could
// type would silently un-bill real interactions.

const compactSystemPrefix = "You are a helpful AI assistant tasked with summarizing conversations"

const (
	compactInstruction = "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."
	compactTasksMarker = "Pending Tasks:"
	compactWorkMarker  = "Current Work:"
)

// isCompactRequest matches the three compact signatures:
//  1. the dedicated compact system prompt prefix (the user cannot set the
//     system prompt — the strongest signal),
//  2. the exact machine instruction in the last user message,
//  3. both structural markers together in the last user message.
func isCompactRequest(m map[string]any) bool {
	if strings.HasPrefix(systemText(m), compactSystemPrefix) {
		return true
	}
	messages := asSlice(m["messages"])
	if len(messages) == 0 {
		return false
	}
	last := asMap(messages[len(messages)-1])
	if last == nil || last["role"] != initiatorUser {
		return false
	}
	text := messageText(last)
	if strings.Contains(text, compactInstruction) {
		return true
	}

	return strings.Contains(text, compactTasksMarker) && strings.Contains(text, compactWorkMarker)
}

// systemText joins the system field (string or text blocks).
func systemText(m map[string]any) string {
	switch sys := m["system"].(type) {
	case string:
		return sys
	case []any:
		parts := make([]string, 0, len(sys))
		for _, b := range sys {
			if blk := asMap(b); blk != nil && blk["type"] == blockTypeText {
				if text := asString(blk["text"]); text != "" {
					parts = append(parts, text)
				}
			}
		}

		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// messageText extracts a message's text (string content or text blocks).
func messageText(msg map[string]any) string {
	switch content := msg["content"].(type) {
	case string:
		return content
	case []any:
		parts := make([]string, 0, len(content))
		for _, b := range content {
			if blk := asMap(b); blk != nil && blk["type"] == blockTypeText {
				if text := asString(blk["text"]); text != "" {
					parts = append(parts, text)
				}
			}
		}

		return strings.Join(parts, "\n")
	default:
		return ""
	}
}
