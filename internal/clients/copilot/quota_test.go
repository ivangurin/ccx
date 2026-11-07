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
	require.ErrorContains(t, err, "auth copilot")
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
	require.ErrorContains(t, err, "auth copilot")
}
