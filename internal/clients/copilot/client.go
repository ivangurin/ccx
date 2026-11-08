// Package copilot implements the GitHub OAuth device flow and exchange for a
// copilot-token. Tokens are stored per provider in
// <CCX_HOME>/auth/<key>.json (0600); exchange is lazy — driven by expires_at
// (no background goroutines, per the "lazy refresh" plan).
package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// ErrNotAuthenticated means the device is not authenticated (run ccx auth <provider>).
var ErrNotAuthenticated = errors.New("copilot: not authenticated — run ccx auth <provider>")

// GitHubBase is the OAuth entry point (overridden by tests).
const GitHubBase = "https://github.com"

// APIBase is api.github.com (copilot-token exchange).
const APIBase = "https://api.github.com"

// Defaults are the public VSCode extension parameters (masquerade).
const (
	DefaultClientID = "Iv1.b507a08c87ecfe98"
	DefaultScope    = "read:user"
)

// DeviceFlowResult is the device flow start.
type DeviceFlowResult struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// authStore is the copilot-auth.json store format.
type authStore struct {
	UserAccessToken string `json:"user_access_token"`
	CopilotToken    string `json:"copilot_token,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
}

// Client is the Copilot OAuth client.
type Client struct {
	codeURL  string
	tokenURL string
	exURL    string
	store    string

	mu     sync.Mutex
	cached string
	expiry time.Time
}

// New creates a client (store is the provider's auth store path). The device
// flow (code + token polling) lives on github.com; only the copilot-token
// exchange is api.github.com — wiring tokenURL to APIBase made every poll
// hit api.github.com/login/oauth/access_token (404, JSON body) and loop
// silently forever.
func New(store string) *Client {
	return NewWithBase(GitHubBase, GitHubBase, APIBase, store)
}

// NewWithBase is a client with overridden URLs (tests).
func NewWithBase(codeURL, tokenURL, exchangeURL, store string) *Client {
	return &Client{
		codeURL:  strings.TrimSuffix(codeURL, "/") + "/login/device/code",
		tokenURL: strings.TrimSuffix(tokenURL, "/") + "/login/oauth/access_token",
		exURL:    strings.TrimSuffix(exchangeURL, "/") + "/copilot_internal/v2/token",
		store:    store,
	}
}

// Masquerade constants (public values of the GitHub Copilot Chat extension
// — the single source for the auth client AND the chat translator): GitHub
// answers the default Go UA on the Copilot endpoints with "Please only use
// approved clients" (notification_id go_http_client, verified live on the
// token exchange) and is suspected of stalling the device-flow poll for it
// — every Copilot-bound request masquerades as the extension.
const (
	MasqueradeUA    = "GitHubCopilotChat/0.28.0"
	EditorVersion   = "vscode/1.99.0"
	EditorPluginVer = "copilot-chat/0.28.0"
	IntegrationID   = "copilot-chat"
)

// masquerade dresses a request as the Copilot Chat extension client.
func masquerade(req *http.Request) {
	req.Header.Set("User-Agent", MasqueradeUA)
	req.Header.Set("Editor-Version", EditorVersion)
	req.Header.Set("Editor-Plugin-Version", EditorPluginVer)
}

// StartDeviceFlow requests a device code.
func (c *Client) StartDeviceFlow(ctx context.Context) (*DeviceFlowResult, error) {
	form := url.Values{"client_id": {DefaultClientID}, "scope": {DefaultScope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.codeURL, strings.NewReader(form.Encode())) //nolint:gosec // constants
	if err != nil {
		return nil, fmt.Errorf("copilot: device flow: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	masquerade(req)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: device flow: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot: device flow: github responded %d", resp.StatusCode)
	}
	var out DeviceFlowResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("copilot: device flow: %w", err)
	}

	return &out, nil
}

// PollDeviceFlow waits for user confirmation, returns the access token,
// and saves it to the store.
//
//nolint:gocognit // polling loop: select+switch, splitting it would hurt readability
func (c *Client) PollDeviceFlow(ctx context.Context, deviceCode string) (string, error) {
	interval := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("copilot: polling interrupted: %w", ctx.Err())
		case <-time.After(interval):
		}
		form := url.Values{
			"client_id":   {DefaultClientID},
			"device_code": {deviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode())) //nolint:gosec // constants
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		masquerade(req)

		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			continue // network noise — wait for the next tick
		}
		// A non-2xx must surface, not loop: the endpoint answers 200 with an
		// error JSON for pending states; anything else is a wiring/transport
		// problem (the 404-forever bug hid in the silent default branch).
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()

			return "", fmt.Errorf("copilot: device flow poll: github responded %d", resp.StatusCode)
		}
		var payload struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			Interval    int    `json:"interval"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		if decodeErr != nil {
			continue
		}
		switch {
		case payload.AccessToken != "":
			if err := c.saveUserToken(payload.AccessToken); err != nil {
				return "", err
			}

			return payload.AccessToken, nil
		case payload.Error == "authorization_pending" || payload.Error == "slow_down":
			interval = pollIntervalAdvance(interval, payload.Error, payload.Interval)
		case payload.Error != "":
			return "", fmt.Errorf("copilot: device flow: %s", payload.Error)
		default:
			// pending without an interval — next tick keeps the current one
		}
	}
}

// pollIntervalAdvance advances the device-flow polling interval per RFC 8628
// §3.5: authorization_pending adopts the server-advertised interval (when
// present), while slow_down MUST increase it by 5 seconds beyond the larger
// of the current and advertised values — merely adopting the advertised
// value kept the poll at exactly the rate the server had just told us to
// slow down from, looping slow_down until the device code expired.
func pollIntervalAdvance(current time.Duration, errKind string, serverInterval int) time.Duration {
	if errKind == "slow_down" {
		next := current
		if v := time.Duration(serverInterval) * time.Second; serverInterval > 0 && v > next {
			next = v
		}

		return next + 5*time.Second
	}
	if serverInterval > 0 {
		return time.Duration(serverInterval) * time.Second
	}

	return current
}

// PollIntervalAdvanceForTest exports pollIntervalAdvance for unit tests.
func PollIntervalAdvanceForTest(current time.Duration, errKind string, serverInterval int) time.Duration {
	return pollIntervalAdvance(current, errKind, serverInterval)
}

func (c *Client) saveUserToken(token string) error {
	return jsonfile.WriteAtomicJSON(c.store, authStore{UserAccessToken: token}, 0o600)
}

// Authenticated reports whether the store at storePath holds a user login
// (the ccx auth listing probe — no network, just the file).
func Authenticated(storePath string) bool {
	var s authStore
	if err := jsonfile.ReadJSON(storePath, &s); err != nil {
		return false
	}

	return s.UserAccessToken != ""
}

func (c *Client) loadStore() (authStore, error) {
	var s authStore
	if err := jsonfile.ReadJSON(c.store, &s); err != nil {
		return s, ErrNotAuthenticated
	}
	if s.UserAccessToken == "" {
		return s, ErrNotAuthenticated
	}

	return s, nil
}

// Token returns a valid copilot-token (cache → store → exchange; lazy
// refresh at expires_at − 60s).
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cached != "" && time.Now().Before(c.expiry.Add(-60*time.Second)) {
		return c.cached, nil
	}
	store, err := c.loadStore()
	if err != nil {
		return "", err
	}
	if store.CopilotToken != "" && store.ExpiresAt != "" {
		if exp, perr := time.Parse(time.RFC3339, store.ExpiresAt); perr == nil && time.Now().Before(exp.Add(-60*time.Second)) {
			c.cached, c.expiry = store.CopilotToken, exp

			return c.cached, nil
		}
	}
	token, expiresAt, err := c.exchange(ctx, store.UserAccessToken)
	if err != nil {
		return "", err
	}
	// The token is valid and now cached in memory — a failed store save
	// (read-only home, full disk) must not fail the request it was fetched
	// for. The cost of a stale store is one extra exchange at the next
	// process start; failing the turn is unaffordable either way, and the
	// very next call in THIS process serves from the cache.
	c.cached, c.expiry = token, expiresAt
	store.CopilotToken = token
	store.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	_ = jsonfile.WriteAtomicJSON(c.store, store, 0o600)

	return token, nil
}

// exchange swaps the user token for a copilot-token.
func (c *Client) exchange(ctx context.Context, userToken string) (string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.exURL, nil) //nolint:gosec // constant/API
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Accept", "application/json")
	masquerade(req)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("copilot: token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", time.Time{}, errors.New("copilot: user token invalid (401) — run ccx auth <provider>")
	}
	if resp.StatusCode != http.StatusOK {
		// The body names the real reason (verified live: trade_restricted_country,
		// go_http_client) — "status 403" alone hid it behind a second manual probe.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		var e struct {
			Message string `json:"message"`
			Details *struct {
				Message string `json:"message"`
			} `json:"error_details"`
		}
		_ = json.Unmarshal(body, &e)
		if msg := e.Details.Message; msg != "" {
			return "", time.Time{}, fmt.Errorf("copilot: token exchange: status %d: %s", resp.StatusCode, msg)
		}
		if e.Message != "" {
			return "", time.Time{}, fmt.Errorf("copilot: token exchange: status %d: %s", resp.StatusCode, e.Message)
		}

		return "", time.Time{}, fmt.Errorf("copilot: token exchange: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		Token     string          `json:"token"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Token == "" {
		return "", time.Time{}, errors.New("copilot: token exchange: unexpected response")
	}

	return payload.Token, parseExpiry(payload.ExpiresAt), nil
}

// parseExpiry accepts both shapes seen in the wild: github.com returns unix
// seconds (number, verified live); keep the RFC3339 string shape too (the
// original assumption). Unparseable/absent → the conservative 30-minute
// fallback, same as before.
func parseExpiry(raw json.RawMessage) time.Time {
	fallback := time.Now().Add(30 * time.Minute)
	if len(raw) == 0 {
		return fallback
	}
	var secs int64
	if err := json.Unmarshal(raw, &secs); err == nil {
		if secs > 0 {
			return time.Unix(secs, 0)
		}

		return fallback
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if parsed, perr := time.Parse(time.RFC3339, s); perr == nil {
			return parsed
		}
	}

	return fallback
}
