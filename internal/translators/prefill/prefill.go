// Package prefill repacks the assistant prefill for gateways that reject a
// final assistant message (GitHub Copilot: 400 "must end with a user message").
// An exact port of rewrite_body from copilot-bridge.py: the precedents and the
// wording of the instructions are preserved 1:1 — they were tuned against the
// live gateway.
package prefill

import (
	"strconv"
	"strings"
)

// Instruction is the continuation instruction text (1:1 with the bridge).

const (
	blockTypeText = "text"
	Instruction   = "[continuation] The previous response was cut off at the text below. " +
		"Continue it exactly from the point of the cut: do not repeat what was written, " +
		"do not comment, continue immediately:\n\n"
)

// NoTextStub is a stub for when the prefill had no text (thinking/empty).
const NoTextStub = "(the previous response was cut off without text — just continue it from where it stopped)"

// Rewrite repacks a prefill request. Policy: the last message FROM the
// assistant of any kind (text/thinking/empty/tool_use) never goes upstream —
// it is replaced with a final user message with an instruction to continue;
// thinking and tool_use are not carried over. Returns (new body, note); the
// input is not mutated.
func Rewrite(body map[string]any) (map[string]any, string) {
	rawMsgs, ok := body["messages"].([]any)
	if !ok || len(rawMsgs) == 0 {
		return body, "no-messages"
	}
	msgs := asMaps(rawMsgs)

	// Claude Code inserts system hook context AT THE END of messages,
	// including after the assistant prefill; look for the assistant before
	// the system tail.
	k := len(msgs)
	for k > 0 && role(msgs[k-1]) == "system" {
		k--
	}
	systemTail := msgs[k:]

	if k == 0 || role(msgs[k-1]) != "assistant" {
		return rewriteNonAssistantFinal(body, msgs, k, systemTail)
	}

	last := msgs[k-1]
	texts, kinds := prefillContent(last["content"])
	note := "prefill:" + strings.Join(kinds, "+")
	if len(systemTail) > 0 {
		note += "+sys-tail:" + strconv.Itoa(len(systemTail))
	}

	payload := Instruction
	if len(texts) > 0 {
		payload += strings.Join(texts, "\n")
	} else {
		payload += NoTextStub
	}

	// the system tail is preserved but placed BEFORE the final user (the
	// gateway requires a user finale; the hook context stays in the dialogue)
	fresh := copyBody(body)
	fresh["messages"] = appendMaps(msgs[:k-1], systemTail,
		map[string]any{"role": "user", "content": payload})

	return fresh, note
}

// rewriteNonAssistantFinal: the finale is not the assistant's — no-op, or
// merging the system tail into the user (the gateway requires a STRICTLY user
// finale — [user, system] is rejected too).
func rewriteNonAssistantFinal(body map[string]any, msgs []map[string]any, k int, tail []map[string]any) (map[string]any, string) {
	if len(tail) == 0 {
		return body, "no-prefill"
	}
	sysTexts := systemTexts(tail)

	if k > 0 && role(msgs[k-1]) == "user" && len(sysTexts) > 0 {
		fresh := copyBody(body)
		fresh["messages"] = appendMaps(msgs[:k-1], nil, mergeIntoUser(msgs[k-1], sysTexts))

		return fresh, "sys-tail-merged:" + strconv.Itoa(len(tail))
	}
	if k == 0 && len(sysTexts) > 0 {
		// messages are only system: put everything into one user message
		fresh := copyBody(body)
		fresh["messages"] = []any{
			map[string]any{"role": "user", "content": strings.Join(sysTexts, "\n\n")},
		}

		return fresh, "sys-only:" + strconv.Itoa(len(tail))
	}

	return body, "no-prefill"
}

// prefillContent extracts the texts and block kinds of the assistant prefill.
//
//nolint:gocognit // exact port of rewrite_body (1:1 with copilot-bridge.py)
func prefillContent(content any) (texts, kinds []string) {
	switch c := content.(type) {
	case string:
		if c != "" {
			texts = append(texts, c)
			kinds = append(kinds, "text")
		} else {
			kinds = append(kinds, "empty")
		}
	case []any:
		for _, raw := range c {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string) //nolint:revive // default "?" below
			if blockType == "" {
				blockType = "?"
			}
			kinds = append(kinds, blockType)
			if blockType == blockTypeText {
				if text, _ := block["text"].(string); text != "" { //nolint:revive // an empty string == no text
					texts = append(texts, text)
				}
			}
		}
	default:
		// other content types — kinds only
	}
	if len(kinds) == 0 {
		kinds = []string{"empty"}
	}

	return texts, kinds
}

// systemTexts returns the texts of system messages (a string or text blocks).
//
//nolint:gocognit // exact port of _system_texts from copilot-bridge.py (1:1)
func systemTexts(systemMsgs []map[string]any) []string {
	var texts []string
	for _, m := range systemMsgs {
		switch c := m["content"].(type) {
		case string:
			if c != "" {
				texts = append(texts, c)
			}
		case []any:
			for _, raw := range c {
				if block, ok := raw.(map[string]any); ok {
					if block["type"] == blockTypeText {
						if text, _ := block["text"].(string); text != "" { //nolint:revive // an empty string == no text //nolint:revive // an empty string == no text
							texts = append(texts, text)
						}
					}
				}
			}
		default:
			// other content is ignored
		}
	}

	return texts
}

// mergeIntoUser appends system texts to a user message (a string or blocks).
func mergeIntoUser(userMsg map[string]any, sysTexts []string) map[string]any {
	merged := copyMap(userMsg)
	add := strings.Join(sysTexts, "\n\n")
	switch c := userMsg["content"].(type) {
	case string:
		if c != "" {
			merged["content"] = c + "\n\n[system context]\n" + add
		} else {
			merged["content"] = "[system context]\n" + add
		}
	case []any:
		merged["content"] = append(append([]any{}, c...),
			map[string]any{"type": "text", "text": "[system context]\n" + add})
	default:
		merged["content"] = "[system context]\n" + add
	}

	return merged
}

func role(msg map[string]any) string {
	r, _ := msg["role"].(string) //nolint:revive // no role == empty string

	return r
}

func asMaps(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}

	return out
}

// appendMaps assembles map messages into []any (order: a, b, tail...).
func appendMaps(a, b []map[string]any, tail ...map[string]any) []any {
	out := make([]any, 0, len(a)+len(b)+len(tail))
	for _, m := range a {
		out = append(out, m)
	}
	for _, m := range b {
		out = append(out, m)
	}
	for _, m := range tail {
		out = append(out, m)
	}

	return out
}

func copyBody(body map[string]any) map[string]any {
	fresh := make(map[string]any, len(body)+1)
	for k, v := range body {
		fresh[k] = v
	}

	return fresh
}

func copyMap(m map[string]any) map[string]any {
	fresh := make(map[string]any, len(m)+1)
	for k, v := range m {
		fresh[k] = v
	}

	return fresh
}
