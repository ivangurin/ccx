package sessionid_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/translators/sessionid"
	"github.com/stretchr/testify/require"
)

func reqWithHeaders(headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return req
}

func TestFromHeaders(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"x-claude-code-session-id", "X-Claude-Code-Session-Id"},
		{"claude-code-session-id", "Claude-Code-Session-Id"},
		{"session_id", "Session-Id"},
		{"x-session-id", "X-Session-Id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := reqWithHeaders(map[string]string{tc.header: "sid-123456789012345678901"})
			require.Equal(t, "sid-123456789012345678901", sessionid.FromRequest(req, nil))
		})
	}
}

// x-session-id is a codex-client convention with short values; a short value
// is not a session — ignore it and keep looking.
func TestShortHeaderIgnored(t *testing.T) {
	req := reqWithHeaders(map[string]string{"X-Session-Id": "short"})
	require.Empty(t, sessionid.FromRequest(req, nil))
}

func TestFromMetadataSessionID(t *testing.T) {
	req := reqWithHeaders(nil)
	meta := []byte(`{"session_id":"meta-sid-123456789012345"}`)
	require.Equal(t, "meta-sid-123456789012345", sessionid.FromRequest(req, meta))
}

// Claude Code's user_id embeds the session after a _session_ marker.
func TestFromMetadataUserID(t *testing.T) {
	req := reqWithHeaders(nil)
	meta := []byte(`{"user_id":"user_6b1f96f0_account_11111111-2222-3333-4444-555555555555_session_99999999-8888-7777-6666-555555555555"}`)
	require.Equal(t, "99999999-8888-7777-6666-555555555555", sessionid.FromRequest(req, meta))
}

func TestHeaderWinsOverMetadata(t *testing.T) {
	req := reqWithHeaders(map[string]string{"X-Claude-Code-Session-Id": "from-header"})
	meta := []byte(`{"session_id":"from-metadata"}`)
	require.Equal(t, "from-header", sessionid.FromRequest(req, meta))
}

func TestNothingFound(t *testing.T) {
	req := reqWithHeaders(nil)
	require.Empty(t, sessionid.FromRequest(req, []byte(`{}`)))
	require.Empty(t, sessionid.FromRequest(req, nil))
	require.Empty(t, sessionid.FromRequest(req, []byte(`not json`)))
}
