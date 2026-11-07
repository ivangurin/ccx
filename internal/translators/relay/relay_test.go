package relay_test

import (
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
		`{"type":"error","error":{"type":"api_error","message":"upstream 502"}}`,
		rec.Body.String())
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
