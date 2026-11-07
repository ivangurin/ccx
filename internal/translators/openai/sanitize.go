package openai

import (
	"encoding/json"

	"github.com/ivangurin/ccx/internal/models"
)

// sanitizeOrphans heals orphan tool_results before conversion: a tool_result
// whose tool_use is not among the pending assistant calls (a compacted/
// truncated history, a resumed session) becomes a text block — a chat tool
// message with a dangling tool_call_id is a guaranteed 400 on strict
// upstreams, while the text still carries the content to the model.
// An orphan with no extractable text is dropped (an empty user turn is noise).
//
// The pairing survives messages that cannot start a new turn: consecutive
// tool-result-only user messages (parallel results split across messages)
// and system hooks the client inserts mid-exchange. A user message that is
// NOT tool-result-only ends the turn and clears the pairing.
func sanitizeOrphans(req *models.MessagesRequest) {
	pendingIDs := map[string]bool{}
	for i := range req.Messages {
		msg := &req.Messages[i]
		switch {
		case msg.Role == roleAssistant:
			pendingIDs = toolUseIDs(msg)
		case msg.Role == "user" && isToolResultOnly(msg):
			msg.Content = sanitizeContent(msg.Content, pendingIDs)
			// pairing stays: the next split result may answer the same calls
		case msg.Role == "user":
			msg.Content = sanitizeContent(msg.Content, pendingIDs)
			pendingIDs = map[string]bool{}
		default:
			// system and unknown roles ride between calls and results —
			// the pairing survives them
		}
	}
}

// isToolResultOnly: the message content is a non-empty array of tool_result
// blocks (a string content is not — the user typed it).
func isToolResultOnly(msg *models.Message) bool {
	var plain string
	if json.Unmarshal(msg.Content, &plain) == nil {
		return false
	}
	var blocks []models.ContentBlock
	if json.Unmarshal(msg.Content, &blocks) != nil || len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			return false
		}
	}

	return true
}

// toolUseIDs collects the tool_use ids of one assistant message.
func toolUseIDs(msg *models.Message) map[string]bool {
	ids := map[string]bool{}
	var blocks []models.ContentBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return ids
	}
	for _, b := range blocks {
		if b.Type == blockToolUse {
			ids[deref(b.ID)] = true
		}
	}

	return ids
}

// sanitizeContent rewrites one message content: orphans become text blocks.
func sanitizeContent(raw json.RawMessage, paired map[string]bool) json.RawMessage {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return raw // string content carries no tool_results
	}
	var blocks []models.ContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return raw // unparseable stays for convertMessage's own handling
	}
	changed := false
	kept := make([]models.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != "tool_result" || paired[deref(b.ToolUseID)] {
			kept = append(kept, b)

			continue
		}
		changed = true
		if text := toolResultText(b); text != "" {
			kept = append(kept, models.ContentBlock{Type: blockText, Text: &text})
		}
	}
	if !changed {
		return raw
	}
	// an unmarshalable re-serialization cannot happen: kept blocks came from
	// a parsed slice of the same shape
	out, _ := json.Marshal(kept) //nolint:errchkjson // parsed blocks re-serialize

	return out
}
