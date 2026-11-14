package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/readguard"
	"github.com/ivangurin/ccx/internal/pkg/reqdump"
	"github.com/ivangurin/ccx/internal/pkg/usagelog"
	"github.com/ivangurin/ccx/internal/translators/relay"
	"github.com/ivangurin/ccx/internal/translators/sessionid"
)

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
