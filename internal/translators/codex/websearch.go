// Package codex: hosted web search translation. The Anthropic server tool
// web_search_* maps onto the Responses API built-in {"type":"web_search"}
// tool (verified live: the ChatGPT backend accepts it and streams
// web_search_call items + url_citation annotations); on the way back the
// calls and citations become the Anthropic server-tool blocks Claude Code's
// WebSearch parses (server_tool_use + web_search_tool_result).
package codex

import (
	"encoding/json"

	"github.com/ivangurin/ccx/internal/models"
)

const (
	// responsesToolWebSearch is the Responses API hosted web search tool.
	responsesToolWebSearch = "web_search"
	// responsesItemWebSearchCall is a hosted search item in output[].
	responsesItemWebSearchCall = "web_search_call"
	// actionSearch marks a web_search_call that issued a query (the backend
	// also emits open_page actions — page fetches, not searches).
	actionSearch = "search"
	// annotationURLCitation annotates output text with a search source.
	annotationURLCitation = "url_citation"

	typeServerToolUse       = "server_tool_use"
	typeWebSearchToolResult = "web_search_tool_result"
	typeSearchResult        = "search_result"
	nameWebSearch           = "web_search"
)

// searchResult is one entry of web_search_tool_result.content. Source is
// the standard Anthropic field; URL/Snippet are duplicated because Claude
// Code's fallback search parser reads those keys instead.
type searchResult struct {
	Type    string     `json:"type"` // search_result
	Source  string     `json:"source"`
	URL     string     `json:"url"`
	Title   string     `json:"title,omitempty"`
	Snippet string     `json:"snippet,omitempty"`
	Content []textPart `json:"content,omitempty"`
}

type textPart struct {
	Type string `json:"type"` // text
	Text string `json:"text"`
}

type webSearchCall struct {
	ID    string
	Query string
}

// webSearchCollector accumulates hosted-search signals across the stream.
// The blocks materialize at response.completed — annotations keep arriving
// until the final message is done, so nothing can be emitted earlier.
type webSearchCollector struct {
	searches []webSearchCall
	results  []searchResult
	seen     map[string]struct{}
}

// onCallDone records a completed web_search_call (query actions only).
func (c *webSearchCollector) onCallDone(id, actionType, query string) {
	if actionType != actionSearch || query == "" {
		return
	}
	c.searches = append(c.searches, webSearchCall{ID: id, Query: query})
}

// onAnnotation records a url_citation, deduped by URL — the model cites the
// same source repeatedly; the first title wins.
func (c *webSearchCollector) onAnnotation(annotationType, url, title string) {
	if annotationType != annotationURLCitation || url == "" {
		return
	}
	if c.seen == nil {
		c.seen = map[string]struct{}{}
	}
	if _, dup := c.seen[url]; dup {
		return
	}
	c.seen[url] = struct{}{}
	c.results = append(c.results, searchResult{
		Type: typeSearchResult, Source: url, URL: url, Title: title, Snippet: title,
		Content: []textPart{{Type: typeText, Text: title}},
	})
}

// webSearchBlock is a block plus the itemID it opens under.
type webSearchBlock struct {
	itemID string
	block  models.AnthropicBlock
}

// blocks: one server_tool_use per search plus one aggregated
// web_search_tool_result tied to the first search. Nil when the stream
// never searched.
func (c *webSearchCollector) blocks() []webSearchBlock {
	if len(c.searches) == 0 && len(c.results) == 0 {
		return nil
	}
	out := make([]webSearchBlock, 0, len(c.searches)+1)
	for _, s := range c.searches {
		input, _ := json.Marshal(map[string]string{"query": s.Query}) //nolint:errchkjson // constant shape
		out = append(out, webSearchBlock{
			itemID: s.ID,
			block:  models.AnthropicBlock{Type: typeServerToolUse, ID: s.ID, Name: nameWebSearch, Input: input},
		})
	}
	if len(c.results) > 0 {
		content, _ := json.Marshal(c.results) //nolint:errchkjson // constant shape
		toolUseID := ""
		if len(c.searches) > 0 {
			toolUseID = c.searches[0].ID
		}
		out = append(out, webSearchBlock{
			itemID: typeWebSearchToolResult,
			block:  models.AnthropicBlock{Type: typeWebSearchToolResult, ToolUseID: toolUseID, Content: content},
		})
	}

	return out
}
