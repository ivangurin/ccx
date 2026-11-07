package copilot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

// fakeGitHub emulates github.com: device-code, access_token (polling),
// copilot_internal/v2/token (copilot-token exchange).
type fakeGitHub struct {
	exchangeCalls atomic.Int32
}

func (f *fakeGitHub) handler(_ *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/device/code", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"DEV123","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`))
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"gho_user","token_type":"bearer"}`))
	})
	mux.HandleFunc("GET /copilot_internal/v2/token", func(w http.ResponseWriter, _ *http.Request) {
		f.exchangeCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// expires_at is unix seconds — the shape github.com really returns
		// (verified live); the RFC3339-string shape was an assumption.
		_, _ = w.Write([]byte(`{"token":"tid_copilot_secret","expires_at":` + futureUnix() + `,"refresh_in":1800}`))
	})

	return mux
}

const userTokenKey = "user_access_token"

func futureUnix() string {
	return strconv.FormatInt(time.Now().Add(1*time.Hour).Unix(), 10)
}

func futureISO() string {
	return `"` + time.Now().Add(1*time.Hour).UTC().Format(time.RFC3339) + `"`
}

func newClient(t *testing.T, gh *httptest.Server, storePath string) *copilot.Client {
	t.Helper()

	return copilot.NewWithBase(gh.URL, gh.URL, gh.URL, storePath)
}

// RFC 8628 §3.5: a slow_down response MUST increase the polling interval by
// 5 seconds (adopting the advertised interval alone kept the poll at the
// rate the server had just rejected — repeated slow_down until expiry).
// authorization_pending adopts the server's interval when present.
func TestPollIntervalAdvance(t *testing.T) {
	// slow_down with no new value: current + 5s
	require.Equal(t, 10*time.Second, copilot.PollIntervalAdvanceForTest(5*time.Second, "slow_down", 0))
	// slow_down repeating the current interval: still +5s
	require.Equal(t, 10*time.Second, copilot.PollIntervalAdvanceForTest(5*time.Second, "slow_down", 5))
	// slow_down advertising a larger interval: max(current, advertised) + 5s
	require.Equal(t, 13*time.Second, copilot.PollIntervalAdvanceForTest(5*time.Second, "slow_down", 8))
	// pending adopts the advertised interval
	require.Equal(t, 3*time.Second, copilot.PollIntervalAdvanceForTest(5*time.Second, "authorization_pending", 3))
	// pending without a value keeps the current interval
	require.Equal(t, 5*time.Second, copilot.PollIntervalAdvanceForTest(5*time.Second, "authorization_pending", 0))
}

func TestDeviceFlow_Auth(t *testing.T) {
	gh := httptest.NewServer((&fakeGitHub{}).handler(t))
	t.Cleanup(gh.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")

	client := newClient(t, gh, store)
	res, err := client.StartDeviceFlow(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ABCD-1234", res.UserCode)
	require.Equal(t, "https://github.com/login/device", res.VerificationURI)

	token, err := client.PollDeviceFlow(context.Background(), res.DeviceCode)
	require.NoError(t, err)
	require.Equal(t, "gho_user", token)

	// user token saved
	var saved map[string]any
	require.NoError(t, jsonfile.ReadJSON(store, &saved))
	require.Equal(t, "gho_user", saved["user_access_token"])
}

func TestToken_LazyExchangeAndCache(t *testing.T) {
	fg := &fakeGitHub{}
	gh := httptest.NewServer(fg.handler(t))
	t.Cleanup(gh.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	// already authenticated
	require.NoError(t, jsonfile.WriteAtomicJSON(store, map[string]any{userTokenKey: "gho_user"}, 0o600))

	client := newClient(t, gh, store)
	tok1, err := client.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tid_copilot_secret", tok1)

	// second call — from cache (a single exchange)
	tok2, err := client.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, tok1, tok2)
	require.Equal(t, int32(1), fg.exchangeCalls.Load())
}

func TestToken_RefreshOnExpiry(t *testing.T) {
	gh := httptest.NewServer((&fakeGitHub{}).handler(t))
	t.Cleanup(gh.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	// copilot-token already expired
	require.NoError(t, jsonfile.WriteAtomicJSON(store, map[string]any{
		"user_access_token": "gho_user",
		"copilot_token":     "tid_old",
		"expires_at":        time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}, 0o600))

	client := newClient(t, gh, store)
	tok, err := client.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tid_copilot_secret", tok)
}

func TestToken_NotAuthenticated(t *testing.T) {
	gh := httptest.NewServer((&fakeGitHub{}).handler(t))
	t.Cleanup(gh.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")

	client := newClient(t, gh, store)
	_, err := client.Token(context.Background())
	require.ErrorIs(t, err, copilot.ErrNotAuthenticated)
}

func TestStore_Permissions600(t *testing.T) {
	gh := httptest.NewServer((&fakeGitHub{}).handler(t))
	t.Cleanup(gh.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	require.NoError(t, jsonfile.WriteAtomicJSON(store, map[string]any{"user_access_token": "x"}, 0o600))

	client := newClient(t, gh, store)
	_, err := client.Token(context.Background())
	require.NoError(t, err)

	info, err := os.Stat(store)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// GitHub answers the default Go UA on the Copilot endpoints with
// "Please only use approved clients" (notification_id go_http_client, seen
// live on the token exchange) — every client request must masquerade as the
// Copilot Chat extension.
func TestClient_MasqueradeHeadersOnAllRequests(t *testing.T) {
	got := map[string]string{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got[r.Method+" "+r.URL.Path] = r.Header.Get("User-Agent") + "|" +
			r.Header.Get("Editor-Version") + "|" + r.Header.Get("Editor-Plugin-Version")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /login/device/code":
			_, _ = w.Write([]byte(`{"device_code":"D","user_code":"U","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`))
		case "POST /login/oauth/access_token":
			_, _ = w.Write([]byte(`{"access_token":"gho_user","token_type":"bearer"}`))
		default:
			_, _ = w.Write([]byte(`{"token":"tid","expires_at":` + futureISO() + `}`))
		}
	}))
	t.Cleanup(up.Close)

	client := copilot.NewWithBase(up.URL, up.URL, up.URL, filepath.Join(t.TempDir(), "copilot-auth.json"))
	res, err := client.StartDeviceFlow(context.Background())
	require.NoError(t, err)
	_, err = client.PollDeviceFlow(context.Background(), res.DeviceCode)
	require.NoError(t, err)
	_, err = client.Token(context.Background())
	require.NoError(t, err)

	want := "GitHubCopilotChat/0.28.0|vscode/1.99.0|copilot-chat/0.28.0"
	for _, path := range []string{
		"POST /login/device/code",
		"POST /login/oauth/access_token",
		"GET /copilot_internal/v2/token",
	} {
		require.Equal(t, want, got[path], path)
	}
}

// The exchange error must carry the upstream reason: the live 403 body names
// the cause ("Copilot is not available in your location",
// notification_id trade_restricted_country) — "status 403" alone hid it.
func TestExchange_ErrorIncludesBodyMessage(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error_details":{"message":"At this time, Copilot is not available in your location.","notification_id":"trade_restricted_country"}}`))
	}))
	t.Cleanup(up.Close)

	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	require.NoError(t, jsonfile.WriteAtomicJSON(store, map[string]string{userTokenKey: "gho_u"}, 0o600))
	client := copilot.NewWithBase(up.URL, up.URL, up.URL, store)

	_, err := client.Token(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Copilot is not available")
	require.Contains(t, err.Error(), "403")
}

func TestAuthenticated(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "ok.json")
	require.NoError(t, jsonfile.WriteAtomicJSON(valid, map[string]string{"user_access_token": "u"}, 0o600))
	empty := filepath.Join(dir, "empty.json")
	require.NoError(t, jsonfile.WriteAtomicJSON(empty, map[string]string{"user_access_token": ""}, 0o600))

	require.True(t, copilot.Authenticated(valid))
	require.False(t, copilot.Authenticated(empty))
	require.False(t, copilot.Authenticated(filepath.Join(dir, "missing.json")))
}
