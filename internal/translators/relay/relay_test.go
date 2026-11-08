package relay_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/translators/relay"
	"github.com/stretchr/testify/require"
)

func TestWriteErr_AnthropicEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()

	relay.WriteErr(rec, http.StatusBadGateway, "upstream blew up")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t,
		`{"type":"error","error":{"type":"api_error","message":"upstream blew up"}}`,
		rec.Body.String())
}

func TestWriteErr_EscapesMessage(t *testing.T) {
	rec := httptest.NewRecorder()

	relay.WriteErr(rec, http.StatusBadRequest, `say "hi"`)

	require.JSONEq(t,
		`{"type":"error","error":{"type":"api_error","message":"say \"hi\""}}`,
		rec.Body.String())
}

func TestUpstreamError_JSONBodyPassesThrough(t *testing.T) {
	resp := httpResponse(http.StatusTooManyRequests, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	t.Cleanup(func() { _ = resp.Body.Close() })
	rec := httptest.NewRecorder()

	relay.UpstreamError(rec, resp)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, rec.Body.String())
}

func TestUpstreamError_NonJSONBodyIsWrapped(t *testing.T) {
	resp := httpResponse(http.StatusBadGateway, "plain text gateway noise")
	t.Cleanup(func() { _ = resp.Body.Close() })
	rec := httptest.NewRecorder()

	relay.UpstreamError(rec, resp)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.JSONEq(t,
		`{"type":"error","error":{"type":"api_error","message":"upstream 502: plain text gateway noise"}}`,
		rec.Body.String())
}

// The provider's actual reason must survive the wrap — quotes and all: a
// plain-text cause ("rate limited", a quoted reason, an HTML block) is the
// user's only diagnostic, and a huge body is cut to a bounded snippet.
func TestUpstreamError_NonJSONBodyReasonSurvives(t *testing.T) {
	resp := httpResponse(http.StatusForbidden, `denied: "automatization" not allowed`)
	t.Cleanup(func() { _ = resp.Body.Close() })
	rec := httptest.NewRecorder()

	relay.UpstreamError(rec, resp)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t,
		`{"type":"error","error":{"type":"api_error","message":"upstream 403: denied: \"automatization\" not allowed"}}`,
		rec.Body.String())
}

func TestUpstreamError_NonJSONBodyIsCapped(t *testing.T) {
	big := strings.Repeat("x", 100*1024)
	resp := httpResponse(http.StatusTooManyRequests, big)
	t.Cleanup(func() { _ = resp.Body.Close() })
	rec := httptest.NewRecorder()

	relay.UpstreamError(rec, resp)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	// "upstream 429: " + snippet + "…" — the snippet is bounded well below
	// the 64 KiB body cap
	require.Less(t, len(out.Error.Message), 700)
	require.True(t, strings.HasSuffix(out.Error.Message, "…"))
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
