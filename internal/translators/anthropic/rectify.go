package anthropic

import (
	"encoding/json"
	"strings"
)

// One rectifying retry on thinking-contract 400s: relay-compatible upstreams
// (Anthropic-shaped, not Anthropic) reject histories Claude Code itself
// accepts — signed thinking blocks the relay cannot verify, thinking budgets
// below its floor. The rejection text names the contract; the fix is local
// and lossless enough (a stripped thinking block or a clamped budget) that
// one retry is worth more than surfacing the error to the user.

// thinkingBudgetFloor is the minimum extended-thinking budget the API family
// accepts; requests below it are the client's, the floor is ours.
const thinkingBudgetFloor = 1024

// rectifyBody decides whether the 400 error text names a fixable
// thinking-contract violation and returns the fixed body. ok=false means
// "not ours to fix" — the caller relays the error untouched.
func rectifyBody(body []byte, errMsg string) ([]byte, bool) {
	msg := lower(errMsg)
	switch {
	case contains(msg, "budget_tokens") && (contains(msg, "at least") || contains(msg, ">=")):
		return clampThinkingBudget(body)
	case contains(msg, "signature") || contains(msg, "thinking block"):
		return stripThinkingBlocks(body)
	default:
		return nil, false
	}
}

// stripThinkingBlocks removes thinking/redacted_thinking blocks from every
// message's content (their signatures are upstream-specific; the model's own
// answer text stays). ok=false when there was nothing to strip.
func stripThinkingBlocks(body []byte) ([]byte, bool) {
	return editMessages(body, func(content []any) ([]any, bool) {
		kept := make([]any, 0, len(content))
		changed := false
		for _, raw := range content {
			if blk, ok := raw.(map[string]any); ok {
				switch blk["type"] {
				case "thinking", "redacted_thinking":
					changed = true

					continue
				default:
					// every other block type stays
				}
			}
			kept = append(kept, raw)
		}
		if !changed {
			return content, false
		}

		return kept, true
	})
}

// clampThinkingBudget lifts thinking.budget_tokens to the floor. ok=false
// when there is no thinking config or it is already at/above the floor.
func clampThinkingBudget(body []byte) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return nil, false
	}
	thinking, ok := m["thinking"].(map[string]any)
	if !ok {
		return nil, false
	}
	budget, ok := thinking["budget_tokens"].(float64)
	if !ok || budget >= thinkingBudgetFloor {
		return nil, false
	}
	thinking["budget_tokens"] = thinkingBudgetFloor
	out, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}

	return out, true
}

// editMessages applies blockEdit to every message's content array and
// re-serializes; ok=false when no message changed (nothing marshaled).
func editMessages(body []byte, blockEdit func(content []any) ([]any, bool)) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return nil, false
	}
	messages, ok := m["messages"].([]any)
	if !ok {
		return nil, false
	}
	changed := false
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := msg["content"].([]any)
		if !ok {
			continue // string content carries no blocks
		}
		next, did := blockEdit(content)
		if !did {
			continue
		}
		msg["content"] = next
		messages[i] = msg
		changed = true
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}

	return out, true
}

func lower(s string) string { return strings.ToLower(s) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }
