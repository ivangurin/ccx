// Package copilot implements the GitHub OAuth device flow and exchange for a
// copilot-token. Tokens are stored in ~/.claude/ccx/copilot-auth.json (600);
// exchange is lazy — driven by expires_at (no background goroutines, per the
// "lazy refresh" plan).
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

// ErrNotAuthenticated means the device is not authenticated (run ccx auth copilot).
var ErrNotAuthenticated = errors.New("copilot: not authenticated — run ccx auth copilot")

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

// New creates a client (store is the copilot-auth.json path).
func New(store string) *Client {
	return NewWithBase(GitHubBase, APIBase, APIBase, store)
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

// StartDeviceFlow requests a device code.
func (c *Client) StartDeviceFlow(ctx context.Context) (*DeviceFlowResult, error) {
	form := url.Values{"client_id": {DefaultClientID}, "scope": {DefaultScope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.codeURL, strings.NewReader(form.Encode())) //nolint:gosec // constants
	if err != nil {
		return nil, fmt.Errorf("copilot: device flow: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

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

		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			continue // network noise — wait for the next tick
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
			if payload.Interval > 0 {
				interval = time.Duration(payload.Interval) * time.Second
			}
		case payload.Error != "":
			return "", fmt.Errorf("copilot: device flow: %s", payload.Error)
		default:
			// pending without an interval — next tick keeps the current one
		}
	}
}

func (c *Client) saveUserToken(token string) error {
	return jsonfile.WriteAtomicJSON(c.store, authStore{UserAccessToken: token}, 0o600)
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
	c.cached, c.expiry = token, expiresAt
	store.CopilotToken = token
	store.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	if err := jsonfile.WriteAtomicJSON(c.store, store, 0o600); err != nil {
		return "", fmt.Errorf("copilot: save token: %w", err)
	}

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

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("copilot: token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", time.Time{}, errors.New("copilot: user token invalid (401) — ccx auth copilot")
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("copilot: token exchange: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Token == "" {
		return "", time.Time{}, errors.New("copilot: token exchange: unexpected response")
	}
	exp := time.Now().Add(30 * time.Minute)
	if payload.ExpiresAt != "" {
		if parsed, perr := time.Parse(time.RFC3339, payload.ExpiresAt); perr == nil {
			exp = parsed
		}
	}

	return payload.Token, exp, nil
}
