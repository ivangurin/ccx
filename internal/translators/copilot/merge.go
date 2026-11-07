package copilot

import "slices"

// Tool-result merging: a user message that mixes tool_results with loose
// text blocks reads to the gateway as a fresh user interaction (and re-bills
// one); skills, edit hooks and plan reminders all send the mixed shape. The
// text is absorbed into the message's tool_results so the whole message is
// pure continuation. Consecutive tool-result-only user messages merge into
// one message for the same reason.

// mergeToolResults returns the body with merged user messages (the input is
// not mutated). Assistant messages and plain-text user messages pass through
// unchanged.
func mergeToolResults(m map[string]any) map[string]any {
	messages := asSlice(m["messages"])
	if len(messages) == 0 {
		return m
	}
	merged := make([]any, 0, len(messages))
	changed := false
	for _, raw := range messages {
		msg := asMap(raw)
		if msg == nil || msg["role"] != initiatorUser {
			merged = append(merged, raw)

			continue
		}
		next, wasMerged := absorbText(msg)
		if wasMerged {
			changed = true
		}
		// consecutive tool-result-only user messages fold into the previous one
		if isToolResultOnly(next) && len(merged) > 0 {
			if prev := asMap(merged[len(merged)-1]); isToolResultOnly(prev) {
				merged[len(merged)-1] = concatContents(prev, next)
				changed = true

				continue
			}
		}
		merged = append(merged, next)
	}
	if !changed {
		return m
	}
	out := shallowCopyMsg(m)
	out["messages"] = merged

	return out
}

// absorbText moves a user message's loose text blocks into its LAST
// tool_result's content; returns the (possibly same) message and whether the
// message is now tool-result-only.
func absorbText(msg map[string]any) (map[string]any, bool) {
	blocks := asSlice(msg["content"])
	var texts []any
	kept := make([]any, 0, len(blocks))
	lastResult := -1
	for _, raw := range blocks {
		blk := asMap(raw)
		if blk == nil {
			kept = append(kept, raw)

			continue
		}
		switch blk["type"] {
		case blockTypeToolResult:
			kept = append(kept, raw)
			lastResult = len(kept) - 1
		case blockTypeText:
			texts = append(texts, map[string]any{"type": blockTypeText, "text": blk["text"]})
		default:
			kept = append(kept, raw)
		}
	}
	if lastResult < 0 || len(texts) == 0 {
		return msg, false
	}
	kept[lastResult] = appendTextForResult(kept[lastResult], texts)
	next := shallowCopyMsg(msg)
	next["content"] = kept

	return next, true
}

// appendTextForResult appends text blocks to a tool_result's content
// (a string content becomes a text-block array first).
func appendTextForResult(rawResult any, texts []any) map[string]any {
	result := asMap(rawResult)
	if result == nil {
		return nil
	}
	blocks := make([]any, 0, len(texts))
	switch content := result["content"].(type) {
	case string:
		blocks = append(blocks, map[string]any{"type": blockTypeText, "text": content})
	case []any:
		blocks = append(blocks, content...)
	default:
		// nil/absent content starts empty — the texts become it
	}
	next := shallowCopyMsg(result)
	next["content"] = append(blocks, texts...)

	return next
}

// isToolResultOnly: every content block is a tool_result.
func isToolResultOnly(msg map[string]any) bool {
	if msg == nil || msg["role"] != initiatorUser {
		return false
	}
	blocks := asSlice(msg["content"])
	if len(blocks) == 0 {
		return false
	}
	for _, raw := range blocks {
		if blk := asMap(raw); blk == nil || blk["type"] != blockTypeToolResult {
			return false
		}
	}

	return true
}

// concatContents merges two tool-result-only user messages into one.
func concatContents(prev, next map[string]any) map[string]any {
	pb := asSlice(prev["content"])
	nb := asSlice(next["content"])
	merged := shallowCopyMsg(prev)
	merged["content"] = slices.Concat(pb, nb)

	return merged
}

func shallowCopyMsg(msg map[string]any) map[string]any {
	out := make(map[string]any, len(msg))
	for k, v := range msg {
		out[k] = v
	}

	return out
}
