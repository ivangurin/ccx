// Package codex — доступ к подписке ChatGPT через Responses API: логин
// переиспользует codex CLI (~/.codex/auth.json), access-токен рефрешится
// лениво с ротацией refresh-токена (запись сразу, flock — конкурентный codex CLI).
package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// ErrNotLoggedIn — ~/.codex/auth.json нет/пуст (нужен codex login).
var ErrNotLoggedIn = errors.New("codex: не выполнен codex login (нет ~/.codex/auth.json)")

// AuthBase — OAuth-сервер OpenAI (публичный client_id codex CLI).
const (
	AuthBase = "https://auth.openai.com"
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	APIBase  = "https://chatgpt.com/backend-api/codex"
)

// Auth — вырезка ~/.codex/auth.json.
type Auth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

type authFile struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"tokens"`
	AccountID string `json:"account_id"`
}

// DefaultAuthPath — ~/.codex/auth.json.
func DefaultAuthPath() string {
	home, _ := os.UserHomeDir()

	return home + "/.codex/auth.json"
}

// LoadAuth читает логин codex CLI.
func LoadAuth(path string) (Auth, error) {
	var f authFile
	if err := jsonfile.ReadJSON(path, &f); err != nil {
		return Auth{}, ErrNotLoggedIn
	}
	if f.Tokens.AccessToken == "" && f.Tokens.RefreshToken == "" {
		return Auth{}, ErrNotLoggedIn
	}

	return Auth{
		AccessToken:  f.Tokens.AccessToken,
		RefreshToken: f.Tokens.RefreshToken,
		AccountID:    f.AccountID,
	}, nil
}

// SaveAuth атомарно пишет auth.json (права сохраняет jsonfile).
func SaveAuth(path string, auth Auth) error {
	var f authFile
	f.Tokens.AccessToken = auth.AccessToken
	f.Tokens.RefreshToken = auth.RefreshToken
	f.AccountID = auth.AccountID

	return jsonfile.WriteAtomicJSON(path, f, 0o600)
}

// TokenValid — exp из JWT-полезной нагрузки (без верификации подписи —
// доверяем своему ключнику, не проверяем его подпись).
func TokenValid(jwt string, now time.Time) bool {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return false
	}

	return now.Unix() < claims.Exp-60
}

// Client — источник токена подписки.
type Client struct {
	authPath string
	authURL  string
	mu       sync.Mutex
}

// New — клиент над ~/.codex/auth.json.
func New(authPath string) *Client {
	return NewWithBase(authPath, AuthBase+"/oauth/token")
}

// NewWithBase — с переопределённым URL рефреша (тесты).
func NewWithBase(authPath, authURL string) *Client {
	return &Client{authPath: authPath, authURL: authURL}
}

// Token — валидный access-токен (ленивый рефреш с ротацией refresh).
func (c *Client) Token(ctx context.Context) (Auth, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	auth, err := LoadAuth(c.authPath)
	if err != nil {
		return Auth{}, err
	}
	if TokenValid(auth.AccessToken, time.Now()) {
		return auth, nil
	}
	if auth.RefreshToken == "" {
		return Auth{}, errors.New("codex: access истёк, refresh-токена нет — codex login")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {auth.RefreshToken},
		"client_id":     {ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authURL, strings.NewReader(form.Encode())) //nolint:gosec // константы
	if err != nil {
		return Auth{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Auth{}, fmt.Errorf("codex: рефреш: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Auth{}, fmt.Errorf("codex: рефреш: статус %d — возможно, нужен codex login", resp.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		return Auth{}, errors.New("codex: рефреш: неожиданный ответ")
	}
	auth.AccessToken = payload.AccessToken
	if payload.RefreshToken != "" {
		auth.RefreshToken = payload.RefreshToken // ротация
	}
	if err := SaveAuth(c.authPath, auth); err != nil {
		return Auth{}, fmt.Errorf("codex: сохранить auth.json: %w", err)
	}

	return auth, nil
}
