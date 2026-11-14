package codex

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

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
