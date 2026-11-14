package tester

import (
	"encoding/json"
	"strings"
)

// extractMessage pulls the human-readable message out of an Anthropic-style
// error body ({...,"error":{"message":"..."}}) or an OpenAI-style one
// ({...,"error":{"message"...}} — same key): the raw JSON on the failure
// line is noise (request ids, codes the user cannot act on). Falls back to
// the trimmed body when it is not JSON or carries no message.
func extractMessage(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "empty error body"
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &body); err == nil && body.Error.Message != "" {
		return body.Error.Message
	}

	return trimmed
}
