// Package codex accesses the ChatGPT subscription via the Responses API:
// login reuses the codex CLI (~/.codex/auth.json), the access token is
// refreshed lazily with refresh-token rotation. Two levels of coexistence:
// ccx writers (the daemon + a CLI command) serialize on a sidecar flock
// <auth.json>.lock; the codex CLI itself ignores that lock, so against it
// the file is governed by GENERATIONS — tokens.last_refresh (RFC3339Nano,
// the token ACQUISITION moment, not the write moment) orders generations: a
// refresh rejection (our refresh token was rotated away underneath us)
// adopts a strictly newer on-disk generation instead of erroring, and a
// save is compare-and-swap — a strictly newer foreign generation on disk is
// never clobbered by ours.
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

	"github.com/ivangurin/ccx/internal/pkg/flock"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// ErrNotLoggedIn means ~/.codex/auth.json is missing/empty (codex login needed).
var ErrNotLoggedIn = errors.New("codex: codex login not performed (no ~/.codex/auth.json)")

// errRefreshRejected marks a 400/401 from the token endpoint — the refresh
// token was refused (rotated away, expired or revoked), which is the adopt
// path's trigger.
var errRefreshRejected = errors.New("refresh rejected")

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
	AccountID    string    // tokens.account_id of the live file
	LastRefresh  time.Time // tokens.last_refresh — the generation clock
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
		LastRefresh:  parseRefreshClock(f.Tokens.LastRefresh),
	}, nil
}

// parseRefreshClock parses tokens.last_refresh; zero when absent/unparseable
// (a zero clock loses to any dated generation — right call for an undated
// file).
func parseRefreshClock(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}

	return ts.UTC()
}

// newerGeneration: strictly newer wins; ties and unknowns lose (refusing to
// act on an ambiguous order is safer than guessing).
func newerGeneration(disk, loaded time.Time) bool {
	return disk.After(loaded)
}

// SaveAuth is read-modify-write: it reads the existing file and patches ONLY
// tokens.access/refresh/last_refresh — auth_mode, id_token, account_id and
// other codex CLI fields survive untouched (invariant: the file belongs to
// someone else, we are guests). If the file is missing, it creates a minimal
// one. The whole read-modify-write runs under the sidecar flock — see the
// package doc.
//
// This is the SEEDING path (writing tokens obtained elsewhere into the
// file). Refresh results must go through Token → saveAuthCAS: this write is
// generation-naive (no compare-and-swap, last_refresh stamped at write time)
// and would clobber a concurrent native-CLI generation.
func SaveAuth(path string, auth Auth) error {
	lock, err := flock.LockWait(path+".lock", authLockTimeout)
	if err != nil {
		return fmt.Errorf("codex: auth lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	return saveAuthLocked(path, auth)
}

// authLockTimeout bounds the wait for a concurrent ccx writer (a refresh
// carries a network call; anything slower than this is stuck, not busy).
const authLockTimeout = 15 * time.Second

func saveAuthLocked(path string, auth Auth) error {
	var f authFile
	_ = jsonfile.ReadJSON(path, &f) // missing file is fine — we will create it
	f.Tokens.AccessToken = auth.AccessToken
	f.Tokens.RefreshToken = auth.RefreshToken
	if f.Tokens.AccountID == "" {
		f.Tokens.AccountID = auth.AccountID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	f.Tokens.LastRefresh = now
	f.LastRefresh = now

	return jsonfile.WriteAtomicJSON(path, f, 0o600)
}

// saveAuthCAS is the generation-aware save for OUR refresh results: if the
// refresh token on disk no longer equals the one we refreshed from, a
// non-cooperating writer (the codex CLI) landed a generation of its own — a
// strictly newer one wins and we do not overwrite; anything else (unchanged,
// or an older foreign write) takes our tokens. last_refresh is the
// acquisition moment, not the write moment — the ordering must survive a
// slow disk while another generation is in flight.
func saveAuthCAS(path string, auth Auth, expectedRefreshToken string, acquiredAt time.Time) error {
	var f authFile
	_ = jsonfile.ReadJSON(path, &f) // missing file is fine — we will create it
	if f.Tokens.RefreshToken != "" && f.Tokens.RefreshToken != expectedRefreshToken {
		if newerGeneration(parseRefreshClock(f.Tokens.LastRefresh), acquiredAt) {
			return nil // the foreign generation stays; ours is already in hand
		}
	}
	f.Tokens.AccessToken = auth.AccessToken
	f.Tokens.RefreshToken = auth.RefreshToken
	if f.Tokens.AccountID == "" {
		f.Tokens.AccountID = auth.AccountID
	}
	clock := acquiredAt.UTC().Format(time.RFC3339Nano)
	f.Tokens.LastRefresh = clock
	f.LastRefresh = clock

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
		return Auth{}, errors.New("codex: access expired, no refresh token — codex login")
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
		return Auth{}, fmt.Errorf("codex: refresh rejected and re-read failed — codex login: %w", err)
	}
	if disk.RefreshToken == loaded.RefreshToken || !newerGeneration(disk.LastRefresh, loaded.LastRefresh) {
		return Auth{}, errors.New("codex: refresh rejected — codex login may be needed")
	}
	if TokenValid(disk.AccessToken, time.Now()) {
		return disk, nil
	}
	if disk.RefreshToken == "" {
		return Auth{}, errors.New("codex: adopted generation has no refresh token — codex login")
	}
	access, rotated, err := c.refresh(ctx, disk.RefreshToken)
	if err != nil {
		return Auth{}, fmt.Errorf("codex: adoptive refresh failed — codex login: %w", err)
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
		return "", "", fmt.Errorf("codex: refresh: status %d — codex login may be needed", resp.StatusCode)
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
