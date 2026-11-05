package codex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/clients/codex"
	"github.com/stretchr/testify/require"
)

// liveAuthJSON — структура РЕАЛЬНОГО ~/.codex/auth.json (tokens.account_id,
// auth_mode, id_token, last_refresh — всё должно переживать наш рефреш).
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
	require.Equal(t, "acc-live-42", auth.AccountID, "account_id живого файла лежит в tokens")
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

	// файл после рефреша: токены обновлены, auth_mode/id_token/account_id/last_refresh сохранены
	rawData, err := os.ReadFile(path) //nolint:gosec // путь песочницы теста
	require.NoError(t, err)
	raw := string(rawData)
	require.Contains(t, raw, `"auth_mode": "chatgpt"`)
	require.Contains(t, raw, "acc-live-42")
	require.Contains(t, raw, `"id_token": "idt"`)
	require.Contains(t, raw, `"refresh_token": "NEW"`)
	require.Contains(t, raw, `"last_refresh"`)
}

func TestC1_AuthTokenEndpointNotRequiredWhenValid(t *testing.T) {
	// валидный access — рефреш не дёргается, поля не трогаются
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
