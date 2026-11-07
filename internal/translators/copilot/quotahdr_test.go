package copilot_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/copilot"
	"github.com/stretchr/testify/require"
)

type hdrCapture struct {
	hdrs http.Header
	body map[string]any
}

func quotaUpstream(t *testing.T) (*httptest.Server, *hdrCapture) {
	t.Helper()
	capt := &hdrCapture{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &capt.body)
		capt.hdrs = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(up.Close)

	return up, capt
}

// A tool continuation rides upstream as agent-initiated with deterministic
// request/interaction ids — the gateway does not re-bill the interaction.
func TestCopilot_QuotaHeadersAgentContinuation(t *testing.T) {
	up, capt := quotaUpstream(t)

	tr := copilot.NewForTest(&models.Profile{}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"out"}]}]}`))
	req.Header.Set("X-Claude-Code-Session-Id", "sess-quota-aaaaaaaaaaaaaaaaa")
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, "agent", capt.hdrs.Get("X-Initiator"))
	require.NotEmpty(t, capt.hdrs.Get("X-Request-ID"))
	require.NotEmpty(t, capt.hdrs.Get("X-Interaction-Id"))
	require.Empty(t, capt.hdrs.Get("X-Interaction-Type"))
	// the merge ran: the tool_result message carries no loose text and the
	// conversation is unchanged in shape
	require.NotContains(t, capt.body, "system")
}

// A user-typed text turn rides as user-initiated (it IS a new interaction).
func TestCopilot_QuotaHeadersUserTurn(t *testing.T) {
	up, capt := quotaUpstream(t)

	tr := copilot.NewForTest(&models.Profile{}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"user","content":"hello there"}]}`))
	req.Header.Set("X-Claude-Code-Session-Id", "sess-quota-bbbbbbbbbbbbbbbb")
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, "user", capt.hdrs.Get("X-Initiator"))
	require.NotEmpty(t, capt.hdrs.Get("X-Request-ID"))
}

// A subagent turn carries the conversation-subagent marker.
func TestCopilot_QuotaHeadersSubagent(t *testing.T) {
	up, capt := quotaUpstream(t)

	tr := copilot.NewForTest(&models.Profile{}, staticToken{token: "t"}, up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-4o","max_tokens":10,"metadata":{"user_id":"user_x_agent_9_session_y"},"messages":[{"role":"user","content":"side quest"}]}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, "agent", capt.hdrs.Get("X-Initiator"))
	require.Equal(t, "conversation-subagent", capt.hdrs.Get("X-Interaction-Type"))
}
