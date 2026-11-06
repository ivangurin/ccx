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

	"github.com/ivangurin/ccms/internal/clients/codex"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/sse"
)

// role/type constants (goconst).
const (
	evError        = "error"
	typeToolUse    = "tool_use"
	typeText       = "text"
	typeMessage    = "message"
	roleAssistantS = "assistant"
)

// codex CLI mimicry (was config: CodexDefaults) — the subscription backend
// expects the codex CLI client, no reason to keep it per-profile.
const (
	codexOriginator = "codex_cli_rs"
	codexUserAgent  = "codex_cli_rs/1.0.0"
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
	ItemID    string          `json:"item_id,omitempty"`
}

// ResponsesTool is a function definition.
type ResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

// ResponsesRequest is POST /codex/responses.
type ResponsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           []ResponsesItem `json:"input"`
	Tools           []ResponsesTool `json:"tools,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
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
	out := &ResponsesRequest{
		Model:           req.Model,
		Instructions:    systemText(req.System),
		MaxOutputTokens: req.MaxTokens,
		Stream:          req.Stream,
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, ResponsesTool{
			Type: "function", Name: tool.Name, Description: tool.Description,
			Parameters: tool.InputSchema,
		})
	}
	for _, msg := range req.Messages {
		out.Input = append(out.Input, convertMessage(msg)...)
	}

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
				Arguments: string(b.Input), CallID: deref(b.ID), ItemID: deref(b.ID),
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
		writeErr(w, http.StatusBadRequest, err.Error())

		return
	}
	body, err := json.Marshal(BuildResponsesRequest(&req))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	auth, err := t.auths.Token(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())

		return
	}
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, t.base+"/responses", bytes.NewReader(body)) //nolint:gosec // base is a constant/test
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

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

	resp, err := t.http.Do(upReq)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())

		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		relayError(w, resp)

		return
	}
	if !req.Stream {
		// the non-stream mode of Responses is not decoded here: Claude Code
		// always streams; return a stub error
		writeErr(w, http.StatusNotImplemented, "codex: non-stream mode is not supported")

		return
	}
	t.serveStream(w, resp, req.Model)
}

// streamEvent is a typed Responses SSE event.
type streamEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta"`
	ItemID string `json:"item_id"`
	Item   *struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		CallID string `json:"call_id"`
		ID     string `json:"id"`
	} `json:"item"`
	Response *struct {
		Usage *models.Usage `json:"usage"`
	} `json:"response"`
}

//nolint:gocognit,gocyclo // linear dispatcher over Responses SSE event types
func (t *Translator) serveStream(w http.ResponseWriter, resp *http.Response, model string) {
	reader := sse.NewReader(resp.Body)
	nextIndex := 0
	itemIdx := map[string]int{}
	itemIsTool := map[string]bool{}
	itemOrder := []string{}
	var usage models.Usage
	stopReason := "end_turn"

	var writer *sse.Writer
	emit := func(ev models.AnthropicEvent) {
		if writer == nil {
			writer = sse.NewWriter(w)
		}
		writer.Event(ev.Type, ev)
	}
	openBlock := func(itemID string, block models.AnthropicBlock) {
		idx := nextIndex
		nextIndex++
		itemIdx[itemID] = idx
		itemIsTool[itemID] = block.Type == typeToolUse
		itemOrder = append(itemOrder, itemID)
		emit(models.AnthropicEvent{Type: "content_block_start", Index: &idx, ContentBlock: &block})
	}

	for {
		ev, err := reader.Next()
		if err != nil {
			// The Responses stream ends with the connection close — there is
			// no [DONE] marker (unlike chat/completions), so a clean io.EOF
			// ends the loop like Done; only a real error is an interruption.
			if errors.Is(err, io.EOF) {
				break
			}
			// break/empty stream before a single event — a plain 502 JSON response
			if writer == nil {
				writeErr(w, http.StatusBadGateway, "codex: empty upstream stream")

				return
			}
			emit(models.AnthropicEvent{
				Type:  "error",
				Error: &models.AnthropicError{Type: "api_error", Message: "upstream stream interrupted"},
			})

			break
		}
		if ev.Done {
			break
		}
		var se streamEvent
		if jsonErr := json.Unmarshal([]byte(ev.Data), &se); jsonErr != nil {
			continue
		}
		if writer == nil { // first event — open the message (headers lazily)
			emit(models.AnthropicEvent{
				Type: "message_start",
				Message: &models.AnthropicMessagePayload{
					ID: "resp", Type: "message", Role: "assistant", Model: model, Content: []any{},
				},
			})
		}
		switch se.Type {
		case "response.output_item.added":
			if se.Item != nil {
				id := se.Item.ID
				if id == "" {
					id = se.Item.CallID
				}
				if se.Item.Type == "function_call" {
					openBlock(id, models.AnthropicBlock{Type: typeToolUse, ID: se.Item.CallID, Name: se.Item.Name})
				} else {
					openBlock(id, models.AnthropicBlock{Type: typeText})
				}
			}
		case "response.output_text.delta":
			if _, ok := itemIdx[se.ItemID]; !ok {
				// a delta without output_item.added — a lazy text block
				openBlock(se.ItemID, models.AnthropicBlock{Type: typeText})
			}
			idx := itemIdx[se.ItemID]
			delta := models.AnthropicDelta{Type: "text_delta", Text: se.Delta}
			emit(models.AnthropicEvent{Type: "content_block_delta", Index: &idx, Delta: &delta})
		case "response.function_call_arguments.delta":
			if idx, ok := itemIdx[se.ItemID]; ok {
				delta := models.AnthropicDelta{Type: "input_json_delta", PartialJSON: se.Delta}
				emit(models.AnthropicEvent{Type: "content_block_delta", Index: &idx, Delta: &delta})
			}
		case "response.completed":
			if se.Response != nil && se.Response.Usage != nil {
				usage = *se.Response.Usage
			}
			for _, id := range itemOrder {
				if itemIsTool[id] {
					stopReason = "tool_use"

					break
				}
			}
		case "response.failed":
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: "response failed"},
			})
		case "error":
			emit(models.AnthropicEvent{
				Type:  evError,
				Error: &models.AnthropicError{Type: "api_error", Message: se.Delta},
			})
		default:
			// other Responses events are ignored
		}
	}
	if writer == nil {
		writeErr(w, http.StatusBadGateway, "codex: empty upstream stream")

		return
	}
	// close ALL blocks in opening order
	for _, id := range itemOrder {
		idx := itemIdx[id]
		emit(models.AnthropicEvent{Type: "content_block_stop", Index: &idx})
	}
	emit(models.AnthropicEvent{
		Type:  "message_delta",
		Delta: &models.AnthropicDelta{Type: "message_delta", StopReason: stopReason},
		Usage: &usage,
	})
	emit(models.AnthropicEvent{Type: "message_stop"})
	writer.Close()
}

func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, err.Error())

		return
	}
	raw, _ := json.Marshal(req) //nolint:errchkjson // RawMessage by construction
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"input_tokens":%d}`, len(raw)/4)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // %q escapes
}

func relayError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if json.Valid(body) {
		_, _ = w.Write(body)

		return
	}
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"upstream %d"}}`, resp.StatusCode)
}
