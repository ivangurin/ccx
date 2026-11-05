// Package copilot — Anthropic → GitHub Copilot: реюз openai-мапперов +
// prefill-фикс (шлюз отвергает финальное assistant) + маскировка под
// VSCode-клиент (шлюз фильтрует посторонних по заголовкам).
package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/chatstream"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/ivangurin/ccx/internal/translators/prefill"
)

// CopilotAPIBase — эндпоинт шлюза Copilot.
const CopilotAPIBase = "https://api.githubcopilot.com"

// TokenSource — источник copilot-token (реализация — clients/copilot с
// ленивым рефрешем; тесты — статический).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Translator — copilot-транслятор.
type Translator struct {
	cfg    *models.CopilotConfig
	tokens TokenSource
	base   string
	http   *http.Client
}

// New строит транслятор (base переопределяется тестами).
func New(profile *models.Profile, tokens TokenSource) *Translator {
	cfg := profile.Copilot
	if cfg == nil {
		cfg = &config.CopilotDefaults
	}
	base := CopilotAPIBase

	return &Translator{cfg: cfg, tokens: tokens, base: base, http: &http.Client{}}
}

// NewForTest — транслятор с переопределённым base-URL.
func NewForTest(cfg *models.CopilotConfig, tokens TokenSource, baseURL string) *Translator {
	return &Translator{cfg: cfg, tokens: tokens, base: baseURL, http: &http.Client{}}
}

// prefillEnabled — nil = true (дефолт): шлюз отвергает prefill без фикса.
func prefillEnabled(cfg *models.CopilotConfig) bool {
	if cfg == nil || cfg.PrefillFix == nil {
		return true
	}

	return *cfg.PrefillFix
}

// Protocol — copilot.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolCopilot
}

// Ready — токен доступен (рефреш при необходимости — в TokenSource).
func (t *Translator) Ready(ctx context.Context) error {
	_, err := t.tokens.Token(ctx)

	return err
}

// Close — нет ресурсов.
func (t *Translator) Close(_ context.Context) error {
	return nil
}

func (t *Translator) headers(token string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	h.Set("X-Github-Api-Version", "2022-11-28")
	if t.cfg.IntegrationID != "" {
		h.Set("Copilot-Integration-Id", t.cfg.IntegrationID)
	}
	if t.cfg.EditorVersion != "" {
		h.Set("Editor-Version", t.cfg.EditorVersion)
	}
	if t.cfg.EditorPlugin != "" {
		h.Set("Editor-Plugin-Version", t.cfg.EditorPlugin)
	}
	if t.cfg.UserAgent != "" {
		h.Set("User-Agent", t.cfg.UserAgent)
	}

	return h
}

// ServeMessages — POST /v1/messages (prefill-фикс → openai-маппинг → шлюз).
func (t *Translator) ServeMessages(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())

		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "невалидное тело: "+err.Error())

		return
	}
	if prefillEnabled(t.cfg) {
		body, _ = prefill.Rewrite(body)
	}
	var req models.MessagesRequest
	if rawMsgs, ok := body["messages"]; ok {
		body["messages"] = rawMsgs // типизация после prefill
	}
	remarshaled, err := json.Marshal(body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	if err := json.Unmarshal(remarshaled, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "невалидное тело: "+err.Error())

		return
	}

	chat := openai.BuildChatRequest(&req, "max_tokens")
	payload, err := json.Marshal(chat) //nolint:errchkjson // RawMessage по построению валиден
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

		return
	}

	token, err := t.tokens.Token(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "copilot-токен недоступен: "+err.Error())

		return
	}
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, t.base+"/chat/completions", bytes.NewReader(payload)) //nolint:gosec // base — константа/тест
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	upReq.Header = t.headers(token)
	resp, err := t.http.Do(upReq)
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
		writeErr(w, http.StatusBadGateway, "невалидный ответ: "+err.Error())

		return
	}
	out := openai.BuildMessagesResponse(&chat, model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if data, merr := json.Marshal(out); merr == nil {
		_, _ = w.Write(data)
	}
}

// ServeCountTokens — локальная оценка.
func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	openai.New(&models.Profile{Protocol: models.ProtocolOpenAI}).ServeCountTokens(w, r)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // %q экранирует
}

// relayUpstreamError копирует ошибку апстрима.
func relayUpstreamError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if json.Valid(body) {
		_, _ = w.Write(body)

		return
	}
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"upstream %d"}}`, resp.StatusCode)
}
