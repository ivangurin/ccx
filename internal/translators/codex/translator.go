// Package codex: Anthropic → ChatGPT Responses API (subscription): system →
// instructions, tool_use → function_call, tool_result → function_call_output;
// Responses SSE events are converted into Anthropic events.
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/pkg/reqdump"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/billing"
	"github.com/ivangurin/ccx/internal/translators/relay"
	"github.com/ivangurin/ccx/internal/translators/sessionid"
)

// role/type constants (goconst).
const (
	evError        = "error"
	evMessageDelta = "message_delta"
	typeToolUse    = "tool_use"
	typeText       = "text"
	typeMessage    = "message"
	roleAssistantS = "assistant"
	roleSystem     = "system"
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
	Output    string          `json:"output,omitempty"` // function_call_output
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

// BuildResponsesRequest maps Messages → Responses.
func BuildResponsesRequest(req *models.MessagesRequest) *ResponsesRequest {
	// Claude Code ships SessionStart-hook output as messages with role
	// "system" inside messages[]; the ChatGPT backend rejects input items
	// with that role ("System messages are not allowed", verified live), so
	// they merge into instructions — the channel Codex CLI uses for all
	// system-level text — after the top-level system prompt.
	instructions := systemText(req.System)
	var systemMsgs []string
	out := &ResponsesRequest{
		Model:           req.Model,
		MaxOutputTokens: req.MaxTokens,
		Stream:          req.Stream,
	}
	for _, tool := range req.Tools {
		switch {
		case tool.IsWebSearch():
			// the hosted search tool (see websearch.go); bare object only
			out.Tools = append(out.Tools, ResponsesTool{Type: responsesToolWebSearch})
		case tool.IsServerTool():
			// other server tools have no hosted counterpart: a phantom
			// function nobody executes is worse than absence — that is
			// exactly how WebSearch used to return empty results
		default:
			out.Tools = append(out.Tools, ResponsesTool{
				Type: "function", Name: tool.Name, Description: tool.Description,
				Parameters: tool.InputSchema,
			})
		}
	}
	for _, msg := range req.Messages {
		if msg.Role == roleSystem {
			// same wire shape as the top-level system field (string | blocks)
			systemMsgs = append(systemMsgs, systemText(msg.Content))

			continue
		}
		out.Input = append(out.Input, convertMessage(msg)...)
	}
	if len(systemMsgs) > 0 {
		parts := append([]string{instructions}, systemMsgs...)
		instructions = strings.Join(parts, "\n\n")
	}
	// the client's local billing line must not reach the instructions
	out.Instructions = billing.Strip(instructions)

	return out
}

func systemText(raw json.RawMessage) string {
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
		if b.Type == typeText {
			parts = append(parts, b.Text)
		}
	}

	return strings.Join(parts, "\n")
}

func convertMessage(msg models.Message) []ResponsesItem {
	var plain string
	if err := json.Unmarshal(msg.Content, &plain); err == nil {
		role := msg.Role
		if role == roleAssistantS {
			role = "assistant"
		}

		return []ResponsesItem{{
			Type: typeMessage, Role: role,
			Content: json.RawMessage(contentJSON(role, plain)),
		}}
	}
	var blocks []models.ContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil
	}

	var out []ResponsesItem
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case typeText:
			if msg.Role == roleAssistantS {
				texts = append(texts, deref(b.Text))
			} else {
				out = append(out, ResponsesItem{
					Type: typeMessage, Role: msg.Role,
					Content: json.RawMessage(contentJSON(msg.Role, deref(b.Text))),
				})
			}
		case "tool_use":
			out = append(out, ResponsesItem{
				Type: "function_call", Name: deref(b.Name),
				Arguments: string(b.Input), CallID: deref(b.ID),
			})
		case "tool_result":
			out = append(out, ResponsesItem{
				Type: "function_call_output", CallID: deref(b.ToolUseID),
				Output: toolResultText(b),
			})
		default:
			// thinking/image and unknown ones are skipped (reasoning is on the Responses side)
		}
	}
	if msg.Role == roleAssistantS && (len(texts) > 0 || len(out) == 0) {
		out = append([]ResponsesItem{{
			Type: typeMessage, Role: "assistant",
			Content: json.RawMessage(contentJSON("assistant", strings.Join(texts, ""))),
		}}, out...)
	}

	return out
}

func contentJSON(role, text string) string {
	field := "input_text"
	if role == roleAssistantS {
		field = "output_text"
	}
	data, _ := json.Marshal([]map[string]string{{"type": field, "text": text}}) //nolint:errchkjson // constant structure

	return string(data)
}

func toolResultText(b models.ContentBlock) string {
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

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// ServeMessages: POST /v1/messages.
func (t *Translator) ServeMessages(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		relay.WriteErr(w, http.StatusBadRequest, err.Error())

		return
	}
	started := time.Now()
	entry := usagelog.Entry{
		Ts:       started,
		Provider: t.profile.Name, Protocol: string(t.Protocol()),
		Model: req.Model, Stream: req.Stream, Status: http.StatusOK,
	}
	defer func() {
		entry.LatencyMS = time.Since(started).Milliseconds()
		usagelog.Record(entry)
	}()
	out := BuildResponsesRequest(&req)
	// the upstream is ALWAYS streamed — the subscription backend contract is
	// stream-only; a non-streaming client is served by aggregation (below)
	out.Stream = true
	// the conversation handle rides as the backend cache key and the codex
	// client session headers — absent when no session is resolvable
	sid := sessionid.FromRequest(r, req.Metadata)
	if sid != "" {
		out.PromptCacheKey = sid
	}
	body, err := json.Marshal(out)
	if err != nil {
		relay.WriteErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	reqdump.Write("codex-req", body)
	auth, err := t.auths.Token(r.Context())
	if err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, err.Error()
		relay.WriteErr(w, http.StatusBadGateway, err.Error())

		return
	}
	// read deadlines: a hung gateway (nothing ever sent) would otherwise
	// leave the client request open forever — see readguard; the upstream is
	// always streamed here, so the first-token patience is always right
	gctx, guard := readguard.Arm(r.Context(), readguard.FirstByte, readguard.Idle)
	defer guard.Stop()
	upReq, err := http.NewRequestWithContext(gctx, http.MethodPost, t.base+"/responses", bytes.NewReader(body)) //nolint:gosec // base is a constant/test
	if err != nil {
		relay.WriteErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	upReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	upReq.Header.Set("Content-Type", "application/json")
	if auth.AccountID != "" {
		upReq.Header.Set("Chatgpt-Account-Id", auth.AccountID)
	}
	// CLI mimicry is a constant: the backend expects the codex CLI client.
	upReq.Header.Set("Originator", codexOriginator)
	upReq.Header.Set("User-Agent", codexUserAgent)
	upReq.Header.Set("Version", codexVersion)
	if sid != "" {
		upReq.Header.Set("Session-Id", sid)
		upReq.Header.Set("X-Client-Request-Id", sid)
		upReq.Header.Set("X-Codex-Window-Id", sid+":0")
	}

	resp, err := t.http.Do(upReq)
	if err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, guard.Err(err).Error()
		relay.WriteErr(w, http.StatusBadGateway, entry.Error)

		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		entry.Status = resp.StatusCode
		relayError(w, resp)

		return
	}
	resp.Body = guard.Body(resp.Body)
	if req.Stream {
		t.serveStream(w, resp, req.Model, &entry)

		return
	}
	t.serveAggregate(w, resp, req.Model, &entry)
}

func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		relay.WriteErr(w, http.StatusBadRequest, err.Error())

		return
	}
	raw, _ := json.Marshal(req) //nolint:errchkjson // RawMessage by construction
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"input_tokens":%d}`, len(raw)/4)
}

// relayError relays an upstream failure with a reqdump of the rejected body —
// codex-specific debugging: the paired codex-req dump shows what triggered it.
// The envelope logic is the shared relay.UpstreamError (body re-armed after
// the dump read it).
func relayError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	reqdump.Write(fmt.Sprintf("codex-resp-%d", resp.StatusCode), body)
	resp.Body = io.NopCloser(strings.NewReader(string(body)))

	relay.UpstreamError(w, resp)
}
