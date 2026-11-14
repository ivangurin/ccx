// Package codex accesses the ChatGPT subscription via the Responses API:
// login is ccx's own ChatGPT OAuth flow (login.go) writing the provider's
// auth store, and the access token is refreshed lazily with refresh-token
// rotation. The store is ccx-owned, but TWO ccx processes can touch the
// same file (the daemon's refresh and a concurrent ccx auth), so writers
// serialize on a sidecar flock <auth.json>.lock and the file is governed by
// GENERATIONS — tokens.last_refresh (RFC3339Nano, the token ACQUISITION
// moment, not the write moment) orders generations: a refresh rejection
// (our refresh token was rotated away underneath us) adopts a strictly
// newer on-disk generation instead of erroring, and a save is
// compare-and-swap — a strictly newer foreign generation on disk is never
// clobbered by ours. The format stays auth.json-compatible: a hand-made
// symlink of the store onto the codex CLI's own file keeps working.
//
// One operation per file: client.go (the Client + its lazy refresh),
// auth_store.go (the auth.json store), token_valid.go, save_cas.go (the
// generation-aware save), login.go (the OAuth PKCE flow).
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/flock"
)

// AuthBase is OpenAI's OAuth server (the codex CLI's public client_id).
const (
	AuthBase = "https://auth.openai.com"
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	APIBase  = "https://chatgpt.com/backend-api/codex"
)

// errRefreshRejected marks a 400/401 from the token endpoint — the refresh
// token was refused (rotated away, expired or revoked), which is the adopt
// path's trigger.
var errRefreshRejected = errors.New("refresh rejected")

// Client is the subscription token source.
type Client struct {
	authPath string
	authURL  string
	mu       sync.Mutex
}

// New is a client over a provider's auth store path.
func New(authPath string) *Client {
	return NewWithBase(authPath, AuthBase+"/oauth/token")
}

// NewWithBase uses an overridden refresh URL (tests).
func NewWithBase(authPath, authURL string) *Client {
	return &Client{authPath: authPath, authURL: authURL}
}

// Token returns a valid access token (lazy refresh with refresh rotation).
// The load→refresh→save span runs under the sidecar flock (the c.mu mutex
// only serializes goroutines of THIS process; the daemon and a CLI command
// are separate processes). Against the codex CLI — which takes no lock —
// refresh rejections adopt a strictly newer on-disk generation (see the
// package doc) before giving up.
func (c *Client) Token(ctx context.Context) (Auth, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lock, err := flock.LockWait(c.authPath+".lock", authLockTimeout)
	if err != nil {
		return Auth{}, fmt.Errorf("codex: auth lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	auth, err := LoadAuth(c.authPath)
	if err != nil {
		return Auth{}, err
	}
	if TokenValid(auth.AccessToken, time.Now()) {
		return auth, nil
	}
	if auth.RefreshToken == "" {
		return Auth{}, errors.New("codex: access expired, no refresh token — run ccx auth <provider>")
	}

	access, rotated, err := c.refresh(ctx, auth.RefreshToken)
	if errors.Is(err, errRefreshRejected) {
		adopted, adoptErr := c.adoptNewerGeneration(ctx, auth)
		if adoptErr != nil {
			return Auth{}, adoptErr
		}

		return adopted, nil
	}
	if err != nil {
		return Auth{}, err
	}

	return c.saveRefreshed(auth, access, rotated)
}

// adoptNewerGeneration handles a rejected refresh: if the file meanwhile
// gained a strictly newer foreign generation (the codex CLI refreshed
// concurrently — it takes no lock), adopt it; when its access token is also
// expired, refresh ONCE with the adopted refresh token.
func (c *Client) adoptNewerGeneration(ctx context.Context, loaded Auth) (Auth, error) {
	disk, err := LoadAuth(c.authPath)
	if err != nil {
		return Auth{}, fmt.Errorf("codex: refresh rejected and re-read failed — run ccx auth <provider>: %w", err)
	}
	if disk.RefreshToken == loaded.RefreshToken || !newerGeneration(disk.LastRefresh, loaded.LastRefresh) {
		return Auth{}, errors.New("codex: refresh rejected — run ccx auth <provider>")
	}
	if TokenValid(disk.AccessToken, time.Now()) {
		return disk, nil
	}
	if disk.RefreshToken == "" {
		return Auth{}, errors.New("codex: adopted generation has no refresh token — run ccx auth <provider>")
	}
	access, rotated, err := c.refresh(ctx, disk.RefreshToken)
	if err != nil {
		return Auth{}, fmt.Errorf("codex: adoptive refresh failed — run ccx auth <provider>: %w", err)
	}

	return c.saveRefreshed(disk, access, rotated)
}

// saveRefreshed CAS-saves a successful refresh (expected = the token we
// refreshed FROM) and returns the refreshed auth: the loaded auth's other
// fields (account_id) carry through untouched.
func (c *Client) saveRefreshed(loaded Auth, access, rotated string) (Auth, error) {
	out := loaded
	out.AccessToken = access
	if rotated != "" {
		out.RefreshToken = rotated // rotation
	}
	acquiredAt := time.Now().UTC()
	if err := saveAuthCAS(c.authPath, out, loaded.RefreshToken, acquiredAt); err != nil {
		return Auth{}, fmt.Errorf("codex: save auth.json: %w", err)
	}
	out.LastRefresh = acquiredAt

	return out, nil
}

// refresh exchanges a refresh token at the token endpoint; a 400/401 answer
// wraps errRefreshRejected (the adopt trigger), anything else is a plain
// error.
func (c *Client) refresh(ctx context.Context, refreshToken string) (access, rotated string, err error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authURL, strings.NewReader(form.Encode())) //nolint:gosec // constants
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("codex: refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		return "", "", fmt.Errorf("codex: refresh: status %d: %w", resp.StatusCode, errRefreshRejected)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("codex: refresh: status %d — run ccx auth <provider>", resp.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		return "", "", errors.New("codex: refresh: unexpected response")
	}

	return payload.AccessToken, payload.RefreshToken, nil
}
