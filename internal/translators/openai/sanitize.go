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
// The pending set works by SUBTRACTION: an assistant message refills it, a
// user message with tool_results removes exactly the ids it answers (a mixed
// tool_result+hook-text message answers some calls without ending the turn —
// the split results that follow still pair), and only a user message with no
// tool_results at all (the human typed) ends the turn. System hooks ride
// between calls and results without touching the set.
func sanitizeOrphans(req *models.MessagesRequest) {
	pendingIDs := map[string]bool{}
	for i := range req.Messages {
		msg := &req.Messages[i]
		switch msg.Role {
		case roleAssistant:
			pendingIDs = toolUseIDs(msg)
		case "user":
			msg.Content = sanitizeContent(msg.Content, pendingIDs)
			pendingIDs = subtractAnswered(msg, pendingIDs)
		default:
			// system and unknown roles — the pairing survives them
		}
	}
}

// subtractAnswered removes the ids this message's tool_results answer; a
// message with no tool_results clears the set (the human's turn).
func subtractAnswered(msg *models.Message, pending map[string]bool) map[string]bool {
	var blocks []models.ContentBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return pending // string content: nothing answered, nothing to clear
	}
	answeredAny := false
	for _, b := range blocks {
		if b.Type == "tool_result" && pending[deref(b.ToolUseID)] {
			delete(pending, deref(b.ToolUseID))
			answeredAny = true
		}
	}
	if answeredAny {
		return pending
	}

	return map[string]bool{}
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
