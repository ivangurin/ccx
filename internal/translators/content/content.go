// Package content — the shared Anthropic content-block readers used by
// every translator: extracting the system text, flattening a tool_result's
// content, dereferencing optional block fields. One implementation instead
// of the per-translator copies that had already begun to drift.
package content

import (
	"encoding/json"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
)

// SystemText extracts the system prompt (a bare string or an array of text
// blocks — non-text blocks are skipped).
func SystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}

	return strings.Join(parts, "\n")
}

// ToolResultText flattens a tool_result's content: a bare string as is, an
// array of blocks to its text parts (other kinds are dropped), anything
// unparseable to its raw JSON text.
func ToolResultText(b models.ContentBlock) string {
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b.Content, &blocks); err != nil {
		return string(b.Content)
	}
	parts := make([]string, 0, len(blocks))
	for _, blk := range blocks {
		if blk.Type == "text" {
			parts = append(parts, blk.Text)
		}
	}

	return strings.Join(parts, "\n")
}

// Deref reads an optional string field (nil → "").
func Deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
