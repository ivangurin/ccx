package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

func quotaServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/copilot_internal/user" {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		if r.Header.Get("Authorization") != "Bearer gho_test" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"copilot_plan":"INDIVIDUAL","premium_interactions":{"entitlement":300,"remaining":37,"reset_date":"2026-12-01"}}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func storeWithUserToken(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, jsonfile.WriteAtomicJSON(path, map[string]any{
		"user_access_token": "gho_test",
	}, 0o600))
}

func TestQuota(t *testing.T) {
	srv := quotaServer(t)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	storeWithUserToken(t, store)

	c := NewWithBase(GitHubBase, GitHubBase, srv.URL+"/copkit/token", store)
	q, err := c.Quota(context.Background())
	require.NoError(t, err)
	require.Equal(t, "INDIVIDUAL", q.CopilotPlan)
	require.NotNil(t, q.PremiumInteractions)
	require.Equal(t, 300, q.PremiumInteractions.Entitlement)
	require.Equal(t, 37, q.PremiumInteractions.Remaining)
	require.Equal(t, "2026-12-01", q.PremiumInteractions.ResetDate)
}

// The 2026-11 response shape: the pool lives in the quota_snapshots map
// (with the reset date hoisted to the top level) instead of a top-level
// premium_interactions object. Payload trimmed from a live response.
func TestQuota_Snapshots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/copilot_internal/user" {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"copilot_plan": "individual",
			"quota_reset_date": "2026-12-01",
			"quota_snapshots": {
				"chat": {"unlimited": true, "entitlement": 0, "remaining": 0, "credits_used": 0},
				"completions": {"unlimited": true, "entitlement": 0, "remaining": 0, "credits_used": 0},
				"premium_interactions": {"unlimited": false, "entitlement": 1500, "remaining": -19, "credits_used": 1518}
			}
		}`))
	}))
	t.Cleanup(srv.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	storeWithUserToken(t, store)

	c := NewWithBase(GitHubBase, GitHubBase, srv.URL+"/copkit/token", store)
	q, err := c.Quota(context.Background())
	require.NoError(t, err)
	require.Equal(t, "individual", q.CopilotPlan)
	require.NotNil(t, q.PremiumInteractions)
	require.Equal(t, 1500, q.PremiumInteractions.Entitlement)
	require.Equal(t, -19, q.PremiumInteractions.Remaining)
	require.Equal(t, "2026-12-01", q.PremiumInteractions.ResetDate)
}

// An unlimited premium pool must not surface as invented 0/0 numbers.
func TestQuota_SnapshotsUnlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"copilot_plan": "individual",
			"quota_reset_date": "2026-12-01",
			"quota_snapshots": {
				"premium_interactions": {"unlimited": true, "entitlement": 0, "remaining": 0, "credits_used": 0}
			}
		}`))
	}))
	t.Cleanup(srv.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	storeWithUserToken(t, store)

	c := NewWithBase(GitHubBase, GitHubBase, srv.URL+"/copkit/token", store)
	q, err := c.Quota(context.Background())
	require.NoError(t, err)
	require.NotNil(t, q.PremiumInteractions)
	require.True(t, q.PremiumInteractions.Unlimited)
}

func TestQuota_NoPremiumField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"copilot_plan":"BUSINESS"}`))
	}))
	t.Cleanup(srv.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	storeWithUserToken(t, store)

	c := NewWithBase(GitHubBase, GitHubBase, srv.URL+"/copkit/token", store)
	q, err := c.Quota(context.Background())
	require.NoError(t, err)
	require.Equal(t, "BUSINESS", q.CopilotPlan)
	require.Nil(t, q.PremiumInteractions)
}

func TestQuota_NotAuthenticated(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "copilot-auth.json"))
	_, err := c.Quota(context.Background())
	require.ErrorContains(t, err, "run ccx auth <provider>")
}

func TestQuota_UnauthorizedSurfacesHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		w := http.ResponseWriter(nil)
		_ = w
	}))
	t.Cleanup(srv.Close)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv2.Close)
	store := filepath.Join(t.TempDir(), "copilot-auth.json")
	storeWithUserToken(t, store)

	c := NewWithBase(GitHubBase, GitHubBase, srv2.URL+"/copkit/token", store)
	_, err := c.Quota(context.Background())
	require.ErrorContains(t, err, "run ccx auth <provider>")
}

// The quota errors must point at the per-provider auth command (no store /
// stale token both send the user to `ccx auth <key>`).
func TestQuota_AuthErrorsPointAtProviderAuth(t *testing.T) {
	dir := t.TempDir()

	_, err := New(filepath.Join(dir, "missing.json")).Quota(context.Background())
	require.ErrorContains(t, err, "run ccx auth <provider>")
}
