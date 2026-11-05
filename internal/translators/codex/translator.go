// Package codex — Anthropic → ChatGPT Responses API (подписка): system →
// instructions, tool_use → function_call, tool_result → function_call_output;
// SSE-события Responses конвертятся в Anthropic-события.
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

	"github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/sse"
)

// константы ролей/типов (goconst).
const (
	typeText       = "text"
	typeMessage    = "message"
	roleAssistantS = "assistant"
)

// ResponsesItem — элемент input[] Responses API.
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

// ResponsesTool — определение функции.
type ResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

// ResponsesRequest — POST /codex/responses.
type ResponsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           []ResponsesItem `json:"input"`
	Tools           []ResponsesTool `json:"tools,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
}

// AuthSource — источник токена (реализация — clients/codex.Client).
type AuthSource interface {
	Token(ctx context.Context) (codex.Auth, error)
}

// Translator — codex-транслятор.
type Translator struct {
	cfg   *models.CodexConfig
	auths AuthSource
	base  string
	http  *http.Client
}

// New строит транслятор.
func New(profile *models.Profile, auths AuthSource) *Translator {
	cfg := profile.Codex
	if cfg == nil {
		cfg = &models.CodexConfig{AuthFile: codex.DefaultAuthPath(), Originator: "codex_cli_rs", UserAgent: "codex_cli_rs/1.0"}
	}

	return &Translator{cfg: cfg, auths: auths, base: codex.APIBase, http: &http.Client{}}
}

// NewForTest — с переопределённым base.
func NewForTest(cfg *models.CodexConfig, auths AuthSource, baseURL string) *Translator {
	return &Translator{cfg: cfg, auths: auths, base: baseURL, http: &http.Client{}}
}

// Protocol — codex.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolCodex
}

// Ready — токен доступен.
func (t *Translator) Ready(ctx context.Context) error {
	_, err := t.auths.Token(ctx)

	return err
}

// Close — нет ресурсов.
func (t *Translator) Close(_ context.Context) error {
	return nil
}

// BuildResponsesRequest маппит Messages → Responses.
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
			// thinking/image и неизвестные — пропускаем (reasoning на стороне Responses)
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
	data, _ := json.Marshal([]map[string]string{{"type": field, "text": text}}) //nolint:errchkjson // константная структура

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

// ServeMessages — POST /v1/messages.
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
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, t.base+"/responses", bytes.NewReader(body)) //nolint:gosec // base — константа/тест
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	upReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	upReq.Header.Set("Content-Type", "application/json")
	if auth.AccountID != "" {
		upReq.Header.Set("Chatgpt-Account-Id", auth.AccountID)
	}
	if t.cfg.Originator != "" {
		upReq.Header.Set("Originator", t.cfg.Originator)
	}
	if t.cfg.UserAgent != "" {
		upReq.Header.Set("User-Agent", t.cfg.UserAgent)
	}

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
		// нестримовый режим Responses здесь не декодируется: Claude Code
		// всегда стримит; отдаём ошибку-заглушку
		writeErr(w, http.StatusNotImplemented, "codex: нестримовый режим не поддерживается")

		return
	}
	t.serveStream(w, resp, req.Model)
}

// streamEvent — типизированное событие Responses SSE.
type streamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Item  *struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		CallID string `json:"call_id"`
	} `json:"item"`
	Response *struct {
		Usage *models.Usage `json:"usage"`
	} `json:"response"`
}

//nolint:gocognit,gocyclo // switch по типам SSE-событий Responses — линейный диспетчер
func (t *Translator) serveStream(w http.ResponseWriter, resp *http.Response, model string) {
	writer := sse.NewWriter(w)
	reader := sse.NewReader(resp.Body)
	started := false
	nextIndex := 0
	toolIdx := map[string]int{}
	var usage models.Usage
	stopReason := "end_turn"

	emit := func(ev models.AnthropicEvent) { writer.Event(ev.Type, ev) }
	for {
		ev, err := reader.Next()
		if err != nil || ev.Done {
			break
		}
		var se streamEvent
		if jsonErr := json.Unmarshal([]byte(ev.Data), &se); jsonErr != nil {
			continue
		}
		if !started {
			started = true
			emit(models.AnthropicEvent{
				Type: "message_start",
				Message: &models.AnthropicMessagePayload{
					ID: "resp", Type: typeMessage, Role: "assistant", Model: model, Content: []any{},
				},
			})
		}
		switch se.Type {
		case "response.output_item.added":
			if se.Item != nil {
				blockType := "text"
				if se.Item.Type == "function_call" {
					blockType = "tool_use"
					toolIdx[se.Item.CallID] = nextIndex
				}
				idx := nextIndex
				nextIndex++
				block := models.AnthropicBlock{Type: blockType}
				if se.Item.Type == "function_call" {
					block.ID, block.Name = se.Item.CallID, se.Item.Name
				}
				emit(models.AnthropicEvent{Type: "content_block_start", Index: &idx, ContentBlock: &block})
			}
		case "response.output_text.delta":
			idx := findTextIndex(toolIdx, nextIndex)
			delta := models.AnthropicDelta{Type: "text_delta", Text: se.Delta}
			emit(models.AnthropicEvent{Type: "content_block_delta", Index: &idx, Delta: &delta})
		case "response.function_call_arguments.delta":
			if idx, ok := lastToolIndex(toolIdx); ok {
				delta := models.AnthropicDelta{Type: "input_json_delta", PartialJSON: se.Delta}
				emit(models.AnthropicEvent{Type: "content_block_delta", Index: &idx, Delta: &delta})
			}
		case "response.completed":
			if se.Response != nil && se.Response.Usage != nil {
				usage = *se.Response.Usage
			}
			if len(toolIdx) > 0 {
				stopReason = "tool_use"
			}
		case "response.failed":
			stopReason = "end_turn"
		case "error":
			emit(models.AnthropicEvent{
				Type:  "error",
				Error: &models.AnthropicError{Type: "api_error", Message: se.Delta},
			})
		default:
			// прочие события Responses игнорируем
		}
	}
	if !started {
		writeErr(w, http.StatusBadGateway, "codex: пустой стрим апстрима")

		return
	}
	// закрыть открытые блоки
	seen := map[int]bool{}
	for _, idx := range toolIdx {
		if !seen[idx] {
			seen[idx] = true
			emit(models.AnthropicEvent{Type: "content_block_stop", Index: &idx})
		}
	}
	emit(models.AnthropicEvent{
		Type:  "message_delta",
		Delta: &models.AnthropicDelta{Type: "message_delta", StopReason: stopReason},
		Usage: &usage,
	})
	emit(models.AnthropicEvent{Type: "message_stop", Message: &models.AnthropicMessagePayload{}})
	writer.Close()
}

// findTextIndex — text-блок всегда индекс 0 (первый output_item).
func findTextIndex(_ map[string]int, _ int) int { return 0 }

func lastToolIndex(toolIdx map[string]int) (int, bool) {
	last, ok := -1, false
	for _, v := range toolIdx {
		if v > last {
			last, ok = v, true
		}
	}

	return last, ok
}

// ServeCountTokens — локальная оценка.
func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, err.Error())

		return
	}
	raw, _ := json.Marshal(req) //nolint:errchkjson // RawMessage по построению
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"input_tokens":%d}`, len(raw)/4)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // %q экранирует
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
