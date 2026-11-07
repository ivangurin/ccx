// Package billing strips Claude Code's local billing-accounting line from
// system prompts. The client leads its system prompt with
// "x-anthropic-billing-header: …" — a local artifact that must not travel
// upstream: at best it perturbs the provider's prompt cache prefix, at worst
// strict gateways reject the request over it.
package billing

import "strings"

// marker leads the system prompt when the client emits it.
const marker = "x-anthropic-billing-header:"

// Strip removes the leading billing line (case-insensitive, including its
// newline). Mid-text occurrences stay — those are the user's own words, not
// the client's accounting artifact.
func Strip(s string) string {
	if len(s) < len(marker) || !strings.EqualFold(s[:len(marker)], marker) {
		return s
	}
	rest := s[len(marker):]
	if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
		rest = rest[idx+1:]
	} else {
		rest = ""
	}

	return strings.TrimLeft(rest, "\r\n\t ")
}
