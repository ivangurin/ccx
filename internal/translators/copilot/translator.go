// Package copilot: Anthropic → GitHub Copilot: reuse of openai mappers +
// prefill fix (the gateway rejects a final assistant message) + masquerading
// as a VSCode client (the gateway filters outsiders by headers).
package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/chatstream"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/ivangurin/ccx/internal/translators/prefill"
	"github.com/ivangurin/ccx/internal/translators/relay"
	"github.com/ivangurin/ccx/internal/translators/sessionid"
)

// CopilotAPIBase is the Copilot gateway endpoint.
const CopilotAPIBase = "https://api.githubcopilot.com"

// TokenSource is the source of the copilot-token (the implementation is
// clients/copilot with lazy refresh; tests use a static one).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Translator is the copilot translator.
type Translator struct {
	profile *models.Profile
	tokens  TokenSource
	base    string
	http    *http.Client
}

// New builds a translator (base is overridden by tests). The VSCode
// masquerade is not config: it comes from the clients/copilot constants.
func New(profile *models.Profile, tokens TokenSource) *Translator {
	base := CopilotAPIBase

	return &Translator{profile: profile, tokens: tokens, base: base, http: &http.Client{}}
}

// NewForTest is a translator with an overridden base URL.
func NewForTest(profile *models.Profile, tokens TokenSource, baseURL string) *Translator {
	return &Translator{profile: profile, tokens: tokens, base: baseURL, http: &http.Client{}}
}

// Protocol is copilot.
func (t *Translator) Protocol() models.Protocol {
	return models.ProtocolCopilot
}

// Ready: the token is available (refresh if needed — in TokenSource).
func (t *Translator) Ready(ctx context.Context) error {
	_, err := t.tokens.Token(ctx)

	return err
}

// Close: no resources.
func (t *Translator) Close(_ context.Context) error {
	return nil
}

func (t *Translator) headers(token string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	// No X-Github-Api-Version here: that is a github.com REST header — the
	// Copilot chat gateway answers it with "bad request: invalid apiVersion"
	// (verified live).
	h.Set("Copilot-Integration-Id", copilot_client.IntegrationID)
	h.Set("Editor-Version", copilot_client.EditorVersion)
	h.Set("Editor-Plugin-Version", copilot_client.EditorPluginVer)
	h.Set("User-Agent", copilot_client.MasqueradeUA)

	return h
}

// quotaHeaders adds the billing classification to the masquerade headers:
// the initiator (user bills a premium interaction, agent is a continuation),
// the deterministic request/interaction ids (the gateway aggregates client
// retries sharing an interaction id instead of billing each attempt), and
// the subagent marker (subagent turns run inside the parent interaction).
func (t *Translator) quotaHeaders(h http.Header, cls quotaClass, sid string, body map[string]any) http.Header {
	requestID, interactionID := quotaIDs(sid, body)
	h.Set("X-Initiator", cls.Initiator)
	h.Set("X-Request-ID", requestID)
	h.Set("X-Interaction-Id", interactionID)
	if cls.IsSubagent {
		h.Set("X-Interaction-Type", "conversation-subagent")
	}

	return h
}

// metadataRaw re-serializes the raw metadata value for sessionid (nil
// marshals to "null", which parses to an empty session — fine).
func metadataRaw(body map[string]any) json.RawMessage {
	raw, _ := json.Marshal(body["metadata"]) //nolint:errchkjson // re-serialize of parsed JSON

	return raw
}

// ServeMessages: POST /v1/messages (prefill fix → openai mapping → gateway).
func (t *Translator) ServeMessages(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		relay.WriteErr(w, http.StatusBadRequest, err.Error())

		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		relay.WriteErr(w, http.StatusBadRequest, "invalid body: "+err.Error())

		return
	}
	// The prefill fix is unconditionally on: the Copilot gateway rejects a
	// final assistant message without it (copilot-bridge.py precedent).
	body, _ = prefill.Rewrite(body)
	// Quota marking runs before the merge (the classification reads the
	// request as the client shaped it) and before the unmarshal (the merge
	// works on the raw shape).
	sid := sessionid.FromRequest(r, metadataRaw(body))
	cls := classifyQuota(body)
	// ids derive from the PRE-merge shape: the merge absorbs loose text into
	// tool_results and would erase the text part of the turn seed
	preMerge := body
	body = mergeToolResults(body)
	var req models.MessagesRequest
	remarshaled, err := json.Marshal(body)
	if err != nil {
		relay.WriteErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	if err := json.Unmarshal(remarshaled, &req); err != nil {
		relay.WriteErr(w, http.StatusBadRequest, "invalid body: "+err.Error())

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

	chat := openai.BuildChatRequest(&req, "max_tokens")
	payload, err := json.Marshal(chat) //nolint:errchkjson // RawMessage is valid by construction
	if err != nil {
		relay.WriteErr(w, http.StatusInternalServerError, err.Error())

		return
	}

	token, err := t.tokens.Token(r.Context())
	if err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, "copilot token unavailable: "+err.Error()
		relay.WriteErr(w, http.StatusBadGateway, entry.Error)

		return
	}
	// read deadlines: a hung gateway (nothing ever sent) would otherwise
	// leave the client request open forever — see readguard
	gctx, guard := readguard.Arm(r.Context(), readguard.FirstByte, readguard.Idle)
	defer guard.Stop()
	upReq, err := http.NewRequestWithContext(gctx, http.MethodPost, t.base+"/chat/completions", bytes.NewReader(payload)) //nolint:gosec // base is a constant/test
	if err != nil {
		relay.WriteErr(w, http.StatusInternalServerError, err.Error())

		return
	}
	upReq.Header = t.quotaHeaders(t.headers(token), cls, sid, preMerge)
	resp, err := t.http.Do(upReq)
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
		})

		return
	}
	t.serveJSON(w, resp, req.Model, &entry)
}

func (t *Translator) serveJSON(w http.ResponseWriter, resp *http.Response, model string, entry *usagelog.Entry) {
	var chat models.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		entry.Status, entry.Error = http.StatusBadGateway, "invalid response: "+err.Error()
		relay.WriteErr(w, http.StatusBadGateway, entry.Error)

		return
	}
	entry.InputTokens, entry.OutputTokens = chat.Usage.InputTokens, chat.Usage.OutputTokens
	out := openai.BuildMessagesResponse(&chat, model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if data, merr := json.Marshal(out); merr == nil {
		_, _ = w.Write(data)
	}
}

// ServeCountTokens: local estimation.
func (t *Translator) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	openai.New(&models.Profile{Protocol: models.ProtocolOpenAI}).ServeCountTokens(w, r)
}
