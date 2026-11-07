package codex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

// writeAuthFull writes auth.json with an explicit tokens.last_refresh (the
// generation clock).
func writeAuthFull(t *testing.T, path, access, refresh, lastRefresh string) {
	t.Helper()
	require.NoError(t, jsonfile.WriteAtomicJSON(path, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  access,
			"refresh_token": refresh,
			"account_id":    "acc-1",
			"last_refresh":  lastRefresh,
		},
	}, 0o600))
}

// The native codex CLI ignores our sidecar flock: it refreshes on its own,
// rotating the refresh token under us — our in-flight refresh then 400s.
// A strictly newer on-disk generation is adopted instead of surfacing the
// rejection: its access token works, no re-login needed.
func TestClient_AdoptsNewerForeignGenerationOnRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	writeAuthFull(t, path, pastJWT(t), "rt-ccx", old)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("refresh_token") == "rt-ccx" {
			// the native CLI rotated the file while our refresh was in flight
			writeAuthFull(t, path, futureJWT(t), "rt-native",
				time.Now().UTC().Format(time.RFC3339Nano))
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))

			return
		}
		w.WriteHeader(http.StatusBadRequest) // rt-native must never be sent
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	tok, err := client.Token(context.Background())
	require.NoError(t, err)
	require.True(t, codex.TokenValid(tok.AccessToken, time.Now()), "the adopted token works")
	require.Equal(t, "rt-native", tok.RefreshToken)
}

// An adopted generation whose access token is also expired gets ONE refresh
// with the adopted refresh token.
func TestClient_AdoptedExpiredRefreshesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	writeAuthFull(t, path, pastJWT(t), "rt-ccx", old)

	refreshes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		refreshes++
		switch r.FormValue("refresh_token") {
		case "rt-ccx":
			writeAuthFull(t, path, pastJWT(t), "rt-native",
				time.Now().UTC().Format(time.RFC3339Nano))
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		case "rt-native":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + futureJWT(t) + `","refresh_token":"rt-3"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	tok, err := client.Token(context.Background())
	require.NoError(t, err)
	require.True(t, codex.TokenValid(tok.AccessToken, time.Now()))
	require.Equal(t, 2, refreshes)

	auth, err := codex.LoadAuth(path)
	require.NoError(t, err)
	require.Equal(t, "rt-3", auth.RefreshToken, "the adoptive refresh saved its rotation")
}

// A rejection with an UNCHANGED file is a real logout — surfaced, not
// retried into a loop.
func TestClient_RejectionWithUnchangedFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	writeAuthFull(t, path, pastJWT(t), "rt-dead", old)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	_, err := client.Token(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "login")
	require.Equal(t, 1, hits)
}

// CAS: a foreign generation that lands on disk BEFORE our refresh answer
// must not be clobbered by our write — the strictly newer generation wins,
// while we still return our own fresh tokens (they are valid until it
// rotates again).
func TestClient_CASDoesNotClobberNewerForeignGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	writeAuthFull(t, path, pastJWT(t), "rt-ccx", old)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("refresh_token") == "rt-ccx" {
			// the native CLI wrote its generation just before our answer
			writeAuthFull(t, path, futureJWT(t), "rt-native",
				time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + futureJWT(t) + `","refresh_token":"rt-ccx-2"}`))

			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	tok, err := client.Token(context.Background())
	require.NoError(t, err)
	require.True(t, codex.TokenValid(tok.AccessToken, time.Now()), "our fresh tokens still return")

	auth, err := codex.LoadAuth(path)
	require.NoError(t, err)
	require.Equal(t, "rt-native", auth.RefreshToken, "the newer foreign generation stays on disk")
}

// The generation clock is the token ACQUISITION moment, written as
// RFC3339Nano.
func TestClient_LastRefreshIsAcquisitionTimeNano(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuthFull(t, path, pastJWT(t), "rt-1", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))

	answered := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		answered = time.Now().UTC()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + futureJWT(t) + `","refresh_token":"rt-2"}`))
	}))
	t.Cleanup(srv.Close)

	client := codex.NewWithBase(path, srv.URL)
	_, err := client.Token(context.Background())
	require.NoError(t, err)

	raw := readTokens(t, path)
	lastRefresh, ok := raw["last_refresh"].(string)
	require.True(t, ok, "tokens.last_refresh present")
	require.Contains(t, lastRefresh, "T", "RFC3339 shape")
	parsed, err := time.Parse(time.RFC3339Nano, lastRefresh)
	require.NoError(t, err)
	require.False(t, parsed.Before(answered.Add(-time.Second)), "clock is the acquisition moment, not the write moment")
	require.False(t, parsed.After(time.Now().UTC().Add(time.Second)))
}

// readTokens returns the raw tokens object for format assertions.
func readTokens(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // test sandbox path
	require.NoError(t, err)
	var file map[string]any
	require.NoError(t, json.Unmarshal(raw, &file))
	tokens, ok := file["tokens"].(map[string]any)
	require.True(t, ok, "tokens object present")

	return tokens
}
