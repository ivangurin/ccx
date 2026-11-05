package copilot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivangurin/ccms/internal/clients/copilot"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
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
		_, _ = w.Write([]byte(`{"token":"tid_copilot_secret","expires_at":` + futureISO() + `,"refresh_in":1800}`))
	})

	return mux
}

func futureISO() string {
	return `"` + time.Now().Add(1*time.Hour).UTC().Format(time.RFC3339) + `"`
}

func newClient(t *testing.T, gh *httptest.Server, storePath string) *copilot.Client {
	t.Helper()

	return copilot.NewWithBase(gh.URL, gh.URL, gh.URL, storePath)
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
	require.NoError(t, jsonfile.WriteAtomicJSON(store, map[string]any{"user_access_token": "gho_user"}, 0o600))

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
