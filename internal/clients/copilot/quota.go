package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// QuotaInfo is the subscription's billing state: the plan and the metered
// pool — AI credits under the 2026-06 usage-based billing (token-metered);
// the API still names the field premium_interactions, so the Go names
// follow the wire, not the billing unit.
type QuotaInfo struct {
	CopilotPlan         string
	PremiumInteractions *PremiumQuota // nil when the account has no pool reported
}

// PremiumQuota is the premium interaction pool state.
type PremiumQuota struct {
	Entitlement int    `json:"entitlement"`
	Remaining   int    `json:"remaining"`
	ResetDate   string `json:"reset_date"`

	// Unlimited marks an uncounted pool: the schema reports it for the chat
	// and completions snapshots, so a premium pool may report it on some
	// plans — printing 0/0 for it would invent numbers.
	Unlimited bool
}

// QuotaSnapshot is one entry of the account's quota_snapshots map (chat,
// completions, premium_interactions — same shape for every key).
type QuotaSnapshot struct {
	Entitlement int  `json:"entitlement"`
	Remaining   int  `json:"remaining"`
	CreditsUsed int  `json:"credits_used"`
	Unlimited   bool `json:"unlimited"`
}

// Quota fetches the account's plan and premium interaction pool. The daemon's
// quota marking keeps the burn rate at ~one interaction per user message —
// this is the number that makes the claim checkable.
func (c *Client) Quota(ctx context.Context) (*QuotaInfo, error) {
	store, err := c.loadStore()
	if err != nil {
		return nil, errors.New("copilot: not authenticated — run ccx auth <provider>")
	}
	quotaURL, err := internalUserURL(c.exURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, quotaURL, nil) //nolint:gosec // derived from the configured API base
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+store.UserAccessToken)
	req.Header.Set("Accept", "application/json")
	masquerade(req)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: quota: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("copilot: quota: user token invalid — run ccx auth <provider>")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot: quota: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		CopilotPlan         string                   `json:"copilot_plan"`
		PremiumInteractions *PremiumQuota            `json:"premium_interactions"`
		QuotaResetDate      string                   `json:"quota_reset_date"`
		QuotaSnapshots      map[string]QuotaSnapshot `json:"quota_snapshots"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("copilot: quota: unexpected response")
	}

	// GitHub moved the pool from a top-level premium_interactions object into
	// the quota_snapshots map, with the reset date hoisted to the top level
	// (verified live 2026-11-07). The snapshot shape is the current schema and
	// wins; the legacy top-level object stays parsed for older responses.
	pool := payload.PremiumInteractions
	if s, ok := payload.QuotaSnapshots["premium_interactions"]; ok {
		pool = &PremiumQuota{
			Entitlement: s.Entitlement,
			Remaining:   s.Remaining,
			ResetDate:   payload.QuotaResetDate,
			Unlimited:   s.Unlimited,
		}
	}

	return &QuotaInfo{CopilotPlan: payload.CopilotPlan, PremiumInteractions: pool}, nil
}

// internalUserURL rewrites the token-exchange URL to the account endpoint on
// the same host (tests override the exchange base and get the override).
func internalUserURL(exchangeURL string) (string, error) {
	u, err := url.Parse(exchangeURL)
	if err != nil {
		return "", fmt.Errorf("copilot: quota: bad API base: %w", err)
	}
	u.Path = "/copilot_internal/user"

	return u.String(), nil
}
