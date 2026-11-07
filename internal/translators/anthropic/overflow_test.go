package anthropic_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
	"github.com/stretchr/testify/require"
)

// A body over the passthrough bound is refused loudly (413) — a silent
// truncation would ship broken JSON upstream and surface as a baffling
// provider 400.
func TestProxy_OversizedBodyRefused(t *testing.T) {
	hits := 0
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hits++
	}))
	t.Cleanup(up.Close)

	restore := anthropic.SetMaxProxyBodyForTest(1024)
	defer restore()

	tr := anthropic.New(&models.Profile{Protocol: models.ProtocolAnthropic, BaseURL: up.URL})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"pad":"`+strings.Repeat("x", 2048)+`"}`))
	rec := httptest.NewRecorder()

	tr.ServeMessages(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, 0, hits)
}
