package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/chatstream"
)

// Translator: Anthropic → OpenAI chat/completions.
type Translator struct {
	profile *models.Profile
	http    *http.Client
}

// New builds a translator (client without timeouts: SSE).
func New(profile *models.Profile) *Translator {
	return &Translator{profile: profile, http: &http.Client{}}
}

// Protocol is openai.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolOpenAI
}

// Ready is trivially ready.
func (t *Translator) Ready(_ context.Context) error {
	return nil
}

// Close: no resources.
func (t *Translator) Close(_ context.Context) error {
	return nil
}

// chatURL is base_url + /chat/completions (base is the v1 root).
func (t *Translator) chatURL() string {
	return t.profile.BaseURL + "/chat/completions"
}

// DefaultUserAgent is the upstream UA when the profile sets none — see the
// fallback in do() for why it must not be Go's default.
const DefaultUserAgent = "opencode/latest/2.2.5/cli"

// requestToken extracts the client credential the way Claude Code sends it:
// Authorization: Bearer (ANTHROPIC_AUTH_TOKEN) or x-api-key (ANTHROPIC_API_KEY).
func requestToken(src *http.Request) string {
	if auth := src.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}

	return strings.TrimSpace(src.Header.Get("X-Api-Key"))
}

func (t *Translator) do(src *http.Request, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, t.chatURL(), bytes.NewReader(body)) //nolint:gosec // base_url from the owner config
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// pass-through: the daemon holds no secret of its own — the token the
	// client sent travels upstream as the provider credential.
	if token := requestToken(src); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Without an explicit UA Go leaks "Go-http-client/1.1", which corporate
	// gateways block as automation (one such gateway answers it with
	// "Forbidden to use automatization") — masquerade as opencode's CLI;
	// verbatim UA of opencode 2.2.5.
	req.Header.Set("User-Agent", DefaultUserAgent)
	for k, v := range t.profile.Headers {
		req.Header.Set(k, v)
	}

	return t.http.Do(req)
}

// SerializeForTest exports serialize for tests of the max_tokens_field field.
var SerializeForTest = serialize

// serialize serializes a ChatRequest with max_tokens_field accounted for.
func serialize(chat *models.ChatRequest, field string) ([]byte, error) {
	raw, err := json.Marshal(chat) //nolint:errchkjson // RawMessage is valid JSON by construction
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil { //nolint:errchkjson // map for field renaming
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

// ServeMessages: POST /v1/messages (stream and non-stream).
func (t *Translator) ServeMessages(w http.ResponseWriter, r *http.Request) {
	var req models.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())

		return
	}
	field := t.profile.MaxTokensField
	if field == "" {
		field = "max_tokens"
	}
	body, err := serialize(BuildChatRequest(&req, field), field)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	resp, err := t.do(r, body)
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
		writeErr(w, http.StatusBadGateway, "invalid upstream response: "+err.Error())

		return
	}
	out := BuildMessagesResponse(&chat, model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if data, merr := json.Marshal(out); merr == nil {
		_, _ = w.Write(data)
	}
}

// ServeCountTokens: local estimation (chat/completions has no token endpoint).
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

// estimateTokens is a rough estimate of ~chars/4 (Claude Code's context indicator).
func estimateTokens(req *models.MessagesRequest) int {
	raw, _ := json.Marshal(req) //nolint:errchkjson // RawMessage is valid by construction
	total := len(raw)

	return total / 4
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg) //nolint:gosec // msg is escaped by %q
}

// relayUpstreamError copies the upstream error in the Anthropic format.
func relayUpstreamError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if json.Valid(body) {
		_, _ = w.Write(body) // the error is already JSON — pass it through as is

		return
	}
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"upstream %d"}}`, resp.StatusCode)
}
