// Package codex accesses the ChatGPT subscription via the Responses API:
// login reuses the codex CLI (~/.codex/auth.json), the access token is
// refreshed lazily with refresh-token rotation (written immediately, flock —
// against the concurrent codex CLI).
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

// ErrNotLoggedIn means ~/.codex/auth.json is missing/empty (codex login needed).
var ErrNotLoggedIn = errors.New("codex: codex login not performed (no ~/.codex/auth.json)")

// AuthBase is OpenAI's OAuth server (the codex CLI's public client_id).
const (
	AuthBase = "https://auth.openai.com"
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	APIBase  = "https://chatgpt.com/backend-api/codex"
)

// Auth is the slice of ~/.codex/auth.json we need.
type Auth struct {
	AccessToken  string
	RefreshToken string
	AccountID    string // tokens.account_id of the live file
}

// authFile is the full live auth.json format (codex CLI fields must survive
// our refresh: an RMW patch, not a file recreation).
type authFile struct {
	AuthMode    string `json:"auth_mode"`
	LastRefresh string `json:"last_refresh,omitempty"`
	Tokens      struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
		IDToken      string `json:"id_token"`
		LastRefresh  string `json:"last_refresh"`
	} `json:"tokens"`
}

// DefaultAuthPath is ~/.codex/auth.json.
func DefaultAuthPath() string {
	home, _ := os.UserHomeDir()

	return home + "/.codex/auth.json"
}

// LoadAuth reads the codex CLI login.
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
		AccountID:    f.Tokens.AccountID,
	}, nil
}

// SaveAuth is read-modify-write: it reads the existing file and patches ONLY
// tokens.access/refresh/last_refresh — auth_mode, id_token, account_id and
// other codex CLI fields survive the refresh untouched (invariant: the file
// belongs to someone else, we are guests). If the file is missing, it
// creates a minimal one.
func SaveAuth(path string, auth Auth) error {
	var f authFile
	_ = jsonfile.ReadJSON(path, &f) // missing file is fine — we will create it
	f.Tokens.AccessToken = auth.AccessToken
	f.Tokens.RefreshToken = auth.RefreshToken
	if f.Tokens.AccountID == "" {
		f.Tokens.AccountID = auth.AccountID
	}
	now := time.Now().UTC().Format(time.RFC3339)
	f.Tokens.LastRefresh = now
	f.LastRefresh = now

	return jsonfile.WriteAtomicJSON(path, f, 0o600)
}

// TokenValid reads exp from the JWT payload (no signature verification —
// we trust our own key holder and do not verify its signature).
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

// Client is the subscription token source.
type Client struct {
	authPath string
	authURL  string
	mu       sync.Mutex
}

// New is a client over ~/.codex/auth.json.
func New(authPath string) *Client {
	return NewWithBase(authPath, AuthBase+"/oauth/token")
}

// NewWithBase uses an overridden refresh URL (tests).
func NewWithBase(authPath, authURL string) *Client {
	return &Client{authPath: authPath, authURL: authURL}
}

// Token returns a valid access token (lazy refresh with refresh rotation).
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
		return Auth{}, errors.New("codex: access expired, no refresh token — codex login")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {auth.RefreshToken},
		"client_id":     {ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authURL, strings.NewReader(form.Encode())) //nolint:gosec // constants
	if err != nil {
		return Auth{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Auth{}, fmt.Errorf("codex: refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Auth{}, fmt.Errorf("codex: refresh: status %d — codex login may be needed", resp.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		return Auth{}, errors.New("codex: refresh: unexpected response")
	}
	auth.AccessToken = payload.AccessToken
	if payload.RefreshToken != "" {
		auth.RefreshToken = payload.RefreshToken // rotation
	}
	if err := SaveAuth(c.authPath, auth); err != nil {
		return Auth{}, fmt.Errorf("codex: save auth.json: %w", err)
	}

	return auth, nil
}
