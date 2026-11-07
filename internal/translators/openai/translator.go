package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/chatstream"
	"github.com/ivangurin/ccx/internal/translators/relay"
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

func (t *Translator) do(ctx context.Context, src *http.Request, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.chatURL(), bytes.NewReader(body)) //nolint:gosec // base_url from the owner config
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
		relay.WriteErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())

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

	field := t.profile.MaxTokensField
	if field == "" {
		field = "max_tokens"
	}
	body, err := serialize(BuildChatRequest(&req, field), field)
	if err != nil {
		relay.WriteErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	// read deadlines: a hung gateway (nothing ever sent) would otherwise
	// leave the client request open forever — see readguard; a non-stream
	// answer's first byte IS the finished generation, so it gets the
	// whole-generation budget, not the first-token patience
	gctx, guard := readguard.Arm(r.Context(), readguard.FirstByteFor(req.Stream), readguard.Idle)
	defer guard.Stop()
	resp, err := t.do(gctx, r, body)
	if err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, guard.Err(err).Error()
		relay.WriteErr(w, http.StatusBadGateway, entry.Error)

		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		entry.Status = resp.StatusCode
		relay.UpstreamError(w, resp)

		return
	}
	resp.Body = guard.Body(resp.Body)
	if req.Stream {
		chatstream.ServeChatStream(w, resp, req.Model, func(ttft time.Duration, usage models.Usage) {
			entry.TTFTMS = ttft.Milliseconds()
			entry.InputTokens, entry.OutputTokens = usage.InputTokens, usage.OutputTokens
		}, func(msg string) {
			entry.Status, entry.Error = http.StatusBadGateway, msg
		})

		return
	}
	t.serveJSON(w, resp, req.Model, &entry)
}

func (t *Translator) serveJSON(w http.ResponseWriter, resp *http.Response, model string, entry *usagelog.Entry) {
	var chat models.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, "invalid upstream response: "+err.Error()
		relay.WriteErr(w, http.StatusBadGateway, entry.Error)

		return
	}
	entry.InputTokens, entry.OutputTokens = chat.Usage.InputTokens, chat.Usage.OutputTokens
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
		relay.WriteErr(w, http.StatusBadRequest, err.Error())

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
