package openai

import (
	"encoding/json"

	"github.com/ivangurin/ccx/internal/models"
)

// sanitizeOrphans heals orphan tool_results before conversion: a tool_result
// whose tool_use is not in the immediately preceding assistant message (a
// compacted/truncated history, a resumed session) becomes a text block — a
// chat tool message with a dangling tool_call_id is a guaranteed 400 on
// strict upstreams, while the text still carries the content to the model.
// An orphan with no extractable text is dropped (an empty user turn is noise).
func sanitizeOrphans(req *models.MessagesRequest) {
	prevIDs := map[string]bool{}
	prevWasAssistant := false
	for i := range req.Messages {
		msg := &req.Messages[i]
		if msg.Role == roleAssistant {
			prevIDs = toolUseIDs(msg)
			prevWasAssistant = true

			continue
		}
		if !prevWasAssistant {
			prevIDs = map[string]bool{}
		}
		if msg.Role == "user" {
			msg.Content = sanitizeContent(msg.Content, prevIDs)
		}
		prevIDs, prevWasAssistant = map[string]bool{}, false
	}
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
