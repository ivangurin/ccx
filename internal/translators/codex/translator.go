// Package codex: Anthropic → ChatGPT Responses API (subscription): system →
// instructions, tool_use → function_call, tool_result → function_call_output;
// Responses SSE events are converted into Anthropic events.
//
// One operation per file: translator.go (the wire types + Translator),
// build_request.go (Messages → Responses), serve_messages.go,
// serve_count_tokens.go, stream.go (the SSE event machine), aggregate.go
// (stream → non-stream), websearch.go (the hosted tool's bridge).
package codex

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/models"
)

// role/type constants (goconst).
const (
	evError             = "error"
	evMessageDelta      = "message_delta"
	evContentBlockDelta = "content_block_delta"
	typeToolUse         = "tool_use"
	typeText            = "text"
	typeMessage         = "message"
	roleAssistantS      = "assistant"
	roleSystem          = "system"
)

// codex CLI mimicry (was config: CodexDefaults) — the subscription backend
// expects the codex CLI client, no reason to keep it per-profile. The version
// rides both the Version header and the UA: the backend gates model
// availability by client version, so a stale one locks newer models out of
// the catalog even when the subscription covers them.
const (
	codexOriginator = "codex_cli_rs"
	codexVersion    = "0.159.0"
	codexUserAgent  = codexOriginator + "/" + codexVersion
)

// ResponsesItem is an element of the Responses API input[].
type ResponsesItem struct {
	Type      string          `json:"type"` // message | function_call | function_call_output
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // message: [{type:input_text|output_text,text}]
	Name      string          `json:"name,omitempty"`    // function_call
	Arguments string          `json:"arguments,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	// Output — function_call_output's result, REQUIRED present on that item
	// type even when empty ("Missing required parameter: 'input[N].output'",
	// verified live 2026-11-08) and FORBIDDEN on every other type
	// ("Unknown parameter: 'input[N].output'", verified live the same day —
	// a bare string with omitempty cannot express both, hence the pointer:
	// nil = the field stays off the wire, &"" = an empty result that still
	// serializes). The twin of the arguments:"{}" rule.
	Output *string `json:"output,omitempty"` // function_call_output
	// no item_id here: the backend validates input params strictly
	// ("Unknown parameter: 'input[N].item_id'", verified live); item_id is an
	// SSE-event concept, call_id alone ties a call to its output.
}

// ResponsesTool is a tool definition: a function, or a hosted tool (e.g.
// {"type":"web_search"}) which must serialize bare — the backend validates
// strictly and rejects unknown fields on hosted tools.
type ResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

// ResponsesRequest is POST /codex/responses.
type ResponsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           []ResponsesItem `json:"input"`
	Tools           []ResponsesTool `json:"tools,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	// PromptCacheKey groups a conversation's requests on the backend side:
	// same conversation → same cache. Set from the client session when one
	// is resolvable; empty stays absent (omitempty).
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
	// Store stays false and serializes explicitly (no omitempty): the
	// ChatGPT backend answers anything else with "Store must be set to
	// false" — Codex CLI always sends store:false.
	Store bool `json:"store"`
}

// AuthSource is the token source (implementation — clients/codex.Client).
type AuthSource interface {
	Token(ctx context.Context) (codex.Auth, error)
}

// Translator is the codex translator.
type Translator struct {
	profile *models.Profile
	auths   AuthSource
	base    string
	http    *http.Client
}

// New builds a translator. The codex CLI mimicry (Originator, UA) is not
// config: it is the codexOriginator/codexUserAgent constants; the auth file
// location is resolved by the router.
func New(profile *models.Profile, auths AuthSource) *Translator {
	return &Translator{profile: profile, auths: auths, base: codex.APIBase, http: &http.Client{}}
}

// NewForTest is one with an overridden base.
func NewForTest(profile *models.Profile, auths AuthSource, baseURL string) *Translator {
	return &Translator{profile: profile, auths: auths, base: baseURL, http: &http.Client{}}
}

// Protocol is codex.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolCodex
}

// Ready: the token is available.
func (t *Translator) Ready(ctx context.Context) error {
	_, err := t.auths.Token(ctx)

	return err
}

// Close: no resources.
func (t *Translator) Close(_ context.Context) error {
	return nil
}
