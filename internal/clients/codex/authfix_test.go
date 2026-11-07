package codex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

// liveAuthJSON mirrors the REAL ~/.codex/auth.json shape (tokens.account_id,
// auth_mode, id_token, last_refresh — all of it must survive our refresh).
const liveAuthJSON = `{
	"OPENAI_API_KEY": null,
	"auth_mode": "chatgpt",
	"tokens": {
		"access_token": "ACCESS",
		"refresh_token": "REFRESH",
		"account_id": "acc-live-42",
		"id_token": "idt",
		"last_refresh": "2026-11-01T00:00:00Z"
	},
	"last_refresh": "2026-11-01T00:00:00Z"
}`

func TestC1_LiveAuthShape_AccountIDFromTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, writeFile(path, liveAuthJSON))

	auth, err := codex.LoadAuth(path)
	require.NoError(t, err)
	require.Equal(t, "acc-live-42", auth.AccountID, "the live file's account_id lives in tokens")
}

func TestC1_RefreshPreservesAllFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, writeFile(path, liveAuthJSON))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + futureJWT(t) + `","refresh_token":"NEW"}`))
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	auth, err := client.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "acc-live-42", auth.AccountID)

	// file after refresh: tokens updated, auth_mode/id_token/account_id/last_refresh preserved
	rawData, err := os.ReadFile(path) //nolint:gosec // test sandbox path
	require.NoError(t, err)
	raw := string(rawData)
	require.Contains(t, raw, `"auth_mode": "chatgpt"`)
	require.Contains(t, raw, "acc-live-42")
	require.Contains(t, raw, `"id_token": "idt"`)
	require.Contains(t, raw, `"refresh_token": "NEW"`)
	require.Contains(t, raw, `"last_refresh"`)
}

func TestC1_AuthTokenEndpointNotRequiredWhenValid(t *testing.T) {
	// valid access — refresh not triggered, fields untouched
	path := filepath.Join(t.TempDir(), "auth.json")
	full := `{"auth_mode":"chatgpt","tokens":{"access_token":"` + futureJWT(t) + `","refresh_token":"r","account_id":"a","id_token":"i","last_refresh":"x"}}`
	require.NoError(t, writeFile(path, full))

	client := codex.NewWithBase(path, "http://127.0.0.1:1")
	auth, err := client.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "a", auth.AccountID)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func TestSaveAuth_SeedsAuthModeOnFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth", "cx.json")

	require.NoError(t, codex.SaveAuth(path, codex.Auth{AccessToken: "A", RefreshToken: "R", AccountID: "acc"}))

	var f struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	require.NoError(t, jsonfile.ReadJSON(path, &f))
	require.Equal(t, "chatgpt", f.AuthMode)
	require.Equal(t, "A", f.Tokens.AccessToken)
	require.Equal(t, "R", f.Tokens.RefreshToken)
	require.Equal(t, "acc", f.Tokens.AccountID)
}

func TestSaveAuth_PreservesForeignFields(t *testing.T) {
	// NOT liveAuthJSON: its auth_mode equals the seeded "chatgpt", which
	// cannot detect an overwrite — "apikey" discriminates.
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"auth_mode": "apikey",
		"tokens": {"access_token": "A", "refresh_token": "R",
			"account_id": "acc-live-42", "id_token": "idt"}
	}`), 0o600)) //nolint:gosec // test sandbox

	require.NoError(t, codex.SaveAuth(path, codex.Auth{AccessToken: "NEW-A", RefreshToken: "NEW-R"}))

	var f struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
			IDToken      string `json:"id_token"`
		} `json:"tokens"`
	}
	require.NoError(t, jsonfile.ReadJSON(path, &f))
	require.Equal(t, "apikey", f.AuthMode)              // existing mode survives
	require.Equal(t, "NEW-A", f.Tokens.AccessToken)     // replaced
	require.Equal(t, "NEW-R", f.Tokens.RefreshToken)    // replaced
	require.Equal(t, "acc-live-42", f.Tokens.AccountID) // empty derivation preserves
	require.Equal(t, "idt", f.Tokens.IDToken)
}

func TestAuthenticated(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "ok.json")
	require.NoError(t, codex.SaveAuth(valid, codex.Auth{AccessToken: "A"}))
	empty := filepath.Join(dir, "empty.json")
	require.NoError(t, os.WriteFile(empty, []byte(`{"tokens": {}}`), 0o600)) //nolint:gosec // test sandbox

	require.True(t, codex.Authenticated(valid))
	require.False(t, codex.Authenticated(empty))
	require.False(t, codex.Authenticated(filepath.Join(dir, "missing.json")))
}

// Re-login into a DIFFERENT account must replace account_id: SaveAuth's only
// production caller is the login, which derives the id from the fresh
// login's own id_token — keeping the stored one would send the old account's
// header with the new account's token.
func TestSaveAuth_ReloginReplacesAccountID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(path, []byte(liveAuthJSON), 0o600)) //nolint:gosec // test sandbox

	require.NoError(t, codex.SaveAuth(path, codex.Auth{AccessToken: "B-A", RefreshToken: "B-R", AccountID: "acc-B"}))

	var f struct {
		Tokens struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	require.NoError(t, jsonfile.ReadJSON(path, &f))
	require.Equal(t, "acc-B", f.Tokens.AccountID)
}
