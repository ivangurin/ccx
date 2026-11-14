package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

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
