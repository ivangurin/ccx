package codex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ivangurin/ccms/internal/clients/codex"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

func writeAuth(t *testing.T, path, access, refresh string) {
	t.Helper()
	require.NoError(t, jsonfile.WriteAtomicJSON(path, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  access,
			"refresh_token": refresh,
			"account_id":    "acc-1",
		},
	}, 0o600))
}

func futureJWT(t *testing.T) string {
	t.Helper()
	// header.payload.signature; payload {"exp": <now+1h>} without a signature (we don't verify)
	payload := `{"exp":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`

	return "eyJhbGciOiJKV1QifQ." + b64url(payload) + ".sig"
}

func pastJWT(t *testing.T) string {
	t.Helper()
	payload := `{"exp":` + strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10) + `}`

	return "eyJhbGciOiJKV1QifQ." + b64url(payload) + ".sig"
}

func TestLoadAuth_Missing(t *testing.T) {
	_, err := codex.LoadAuth(filepath.Join(t.TempDir(), "auth.json"))
	require.ErrorIs(t, err, codex.ErrNotLoggedIn)
}

func TestLoadAuth_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuth(t, path, "acc", "ref")

	auth, err := codex.LoadAuth(path)
	require.NoError(t, err)
	require.Equal(t, "acc", auth.AccessToken)
	require.Equal(t, "ref", auth.RefreshToken)
	require.Equal(t, "acc-1", auth.AccountID)
}

func TestTokenExpiry_FromJWT(t *testing.T) {
	require.True(t, codex.TokenValid(futureJWT(t), time.Now()))
	require.False(t, codex.TokenValid(pastJWT(t), time.Now()))
	require.False(t, codex.TokenValid("garbage", time.Now()))
}

func TestClient_Token_ExpiredRefreshes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuth(t, path, pastJWT(t), "old-refresh")

	var gotGrant, gotRefresh string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrant = r.FormValue("grant_type")
		gotRefresh = r.FormValue("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + futureJWT(t) + `","refresh_token":"new-refresh"}`))
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	tok, err := client.Token(context.Background())
	require.NoError(t, err)
	require.True(t, codex.TokenValid(tok.AccessToken, time.Now()))
	require.Equal(t, "refresh_token", gotGrant)
	require.Equal(t, "old-refresh", gotRefresh)

	// rotation saved into auth.json
	auth, err := codex.LoadAuth(path)
	require.NoError(t, err)
	require.Equal(t, "new-refresh", auth.RefreshToken)
}

func TestClient_Token_ValidCached(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	valid := futureJWT(t)
	writeAuth(t, path, valid, "ref")

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	tok, err := client.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, valid, tok.AccessToken)
	require.Equal(t, 0, calls) // refresh not triggered
}

func TestClient_Token_NotLoggedIn(t *testing.T) {
	client := codex.New(filepath.Join(t.TempDir(), "auth.json"))
	_, err := client.Token(context.Background())
	require.ErrorIs(t, err, codex.ErrNotLoggedIn)
}

func TestSaveAuth_PreservesPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuth(t, path, "a", "r")
	auth, err := codex.LoadAuth(path)
	require.NoError(t, err)
	auth.AccessToken = "new"
	require.NoError(t, codex.SaveAuth(path, auth))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// b64url is plain base64url without padding (for the test's JWT payload).
func b64url(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out []byte
	for i := 0; i < len(s); i += 3 {
		var b [3]byte
		n := copy(b[:], s[i:])
		v := uint(b[0])<<16 | uint(b[1])<<8 | uint(b[2])
		out = append(out, alphabet[(v>>18)&63])
		out = append(out, alphabet[(v>>12)&63])
		if n > 1 {
			out = append(out, alphabet[(v>>6)&63])
		}
		if n > 2 {
			out = append(out, alphabet[v&63])
		}
	}

	return string(out)
}
