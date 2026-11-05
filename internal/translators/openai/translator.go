package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/chatstream"
)

// Translator — Anthropic → OpenAI chat/completions.
type Translator struct {
	cfg  *models.OpenAIConfig
	http *http.Client
}

// New строит транслятор (client без таймаутов: SSE).
func New(profile *models.Profile) *Translator {
	return &Translator{cfg: profile.OpenAI, http: &http.Client{}}
}

// Protocol — openai.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolOpenAI
}

// Ready — тривиально готов.
func (t *Translator) Ready(_ context.Context) error {
	return nil
}

// Close — нет ресурсов.
func (t *Translator) Close(_ context.Context) error {
	return nil
}

// chatURL — base_url + /chat/completions (base — корень v1).
func (t *Translator) chatURL() string {
	return t.cfg.BaseURL + "/chat/completions"
}

func (t *Translator) do(body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, t.chatURL(), bytes.NewReader(body)) //nolint:gosec // base_url из конфига владельца
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.cfg.APIKey)
	}
	if t.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", t.cfg.UserAgent) // UA-фильтры корп. шлюзов
	}
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}

	return t.http.Do(req)
}

// SerializeForTest — экспорт serialize для тестов поля max_tokens_field.
var SerializeForTest = serialize

// serialize — ChatRequest с учётом max_tokens_field.
func serialize(chat *models.ChatRequest, field string) ([]byte, error) {
	raw, err := json.Marshal(chat) //nolint:errchkjson // RawMessage — валидный JSON по построению
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil { //nolint:errchkjson // map для переименования поля
		return nil, err
	}
	if field == "max_completion_tokens" {
		if v, ok := m["max_tokens"]; ok {
			m["max_completion_tokens"] = v
			delete(m, "max_tokens")
		}
	}

	return json.Marshal(m)
}

// ServeMessages — POST /v1/messages (стрим и не-стрим).
func (t *Translator) ServeMessages(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "невалидное тело запроса: "+err.Error())

		return
	}
	field := t.cfg.MaxTokensFld
	if field == "" {
		field = "max_tokens"
	}
	body, err := serialize(BuildChatRequest(&req, field), field)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	resp, err := t.do(body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())

		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		relayUpstreamError(w, resp)

		return
	}
	if req.Stream {
		chatstream.ServeChatStream(w, resp, req.Model)

		return
	}
	t.serveJSON(w, resp, req.Model)
}

func (t *Translator) serveJSON(w http.ResponseWriter, resp *http.Response, model string) {
	var chat models.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		writeErr(w, http.StatusBadGateway, "невалидный ответ апстрима: "+err.Error())

		return
	}
	out := BuildMessagesResponse(&chat, model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if data, merr := json.Marshal(out); merr == nil {
		_, _ = w.Write(data)
	}
}

// ServeCountTokens — локальная оценка (у chat/completions нет токен-эндпоинта).
func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())

		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"input_tokens":%d}`, estimateTokens(&req))
}

// estimateTokens — грубая оценка ~chars/4 (индикатор контекста Claude Code).
func estimateTokens(req *models.MessagesRequest) int {
	raw, _ := json.Marshal(req) //nolint:errchkjson // RawMessage по построению валиден
	total := len(raw)

	return total / 4
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // msg экранирован %q
}

// relayUpstreamError копирует ошибку апстрима в Anthropic-формате.
func relayUpstreamError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if json.Valid(body) {
		_, _ = w.Write(body) // ошибка уже JSON — отдаём как есть

		return
	}
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"upstream %d"}}`, resp.StatusCode)
}
