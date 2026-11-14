package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
