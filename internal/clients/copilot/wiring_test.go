package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The device-flow token endpoint lives on github.com, NOT api.github.com:
// New() used to wire tokenURL to APIBase, so every poll hit
// api.github.com/login/oauth/access_token → 404 with a JSON body → decoded
// as an empty payload → the default branch looped silently forever ("Waiting
// for confirmation..." regardless of the user's confirmation — verified
// live three times).
func TestNew_DeviceFlowTokenURLOnGithubDotCom(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "copilot-auth.json"))

	require.True(t, strings.HasPrefix(c.tokenURL, GitHubBase), "token endpoint must be on github.com, got %s", c.tokenURL)
	require.NotContains(t, c.tokenURL, "api.github.com")
	require.True(t, strings.HasPrefix(c.exURL, APIBase), "the exchange stays on api.github.com")
}

// A non-2xx poll response must surface as an error, not loop silently —
// the 404-forever bug hid exactly there.
func TestPollDeviceFlow_Non2xxIsError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found","documentation_url":"https://docs.github.com/rest"}`))
	}))
	t.Cleanup(up.Close)

	c := NewWithBase(up.URL, up.URL, up.URL, filepath.Join(t.TempDir(), "copilot-auth.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second) // > the first 5s poll tick
	defer cancel()

	_, err := c.PollDeviceFlow(ctx, "DEV123")
	require.ErrorContains(t, err, "404")
}
