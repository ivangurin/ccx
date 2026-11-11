// envref tests: a credential field holding an env reference ($NAME) must be
// expanded for the probe too — the structural phase fails the provider
// outright on a missing variable (no daemon lift, fa8099f semantics), and the
// live probe sends the expanded secret as its Bearer, never the literal
// "$NAME".
package tester_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/tester"
	"github.com/stretchr/testify/require"
)

// A missing env variable fails the provider at the structural level: the
// error names the variable, no models were probed.
func TestTest_EnvRefMissing_FailsProviderLevel(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	up := anthropicUpstream(t, "")
	writeProviders(t, sp, newProviders().With("zai", func() *models.Profile {
		p := anthropicProfile(up.URL, models.Model{ID: probeModelID, Default: true})
		p.AuthToken = "$CCX_TEST_MISSING"

		return p
	}()).done())

	report := tester.Run(context.Background(), sp.Config, tester.Options{})

	require.Len(t, report.Providers, 1)
	require.Equal(t, tester.StatusFail, report.Providers[0].Status)
	require.ErrorContains(t, report.Providers[0].Err, "CCX_TEST_MISSING")
	require.Empty(t, report.Providers[0].Models, "no probes fired")
	require.Equal(t, 0, report.ProvidersOK)
}

// The live probe authenticates with the EXPANDED value: the upstream answers
// 401 unless it sees the exact Bearer from the environment.
func TestTest_EnvRef_ExpandedProbeBearer(t *testing.T) {
	t.Setenv("CCX_TEST_TOKEN", "sk-env-probe")
	sp := suite_provider.NewProvider(t)
	up := authCheckingUpstream(t, "Bearer sk-env-probe")
	writeProviders(t, sp, newProviders().With("zai", func() *models.Profile {
		p := anthropicProfile(up.URL, models.Model{ID: probeModelID, Default: true})
		p.AuthToken = "$CCX_TEST_TOKEN"

		return p
	}()).done())

	report := tester.Run(context.Background(), sp.Config, tester.Options{})

	require.Len(t, report.Providers, 1)
	require.NoError(t, report.Providers[0].Err)
	require.Len(t, report.Providers[0].Models, 1)
	require.NoError(t, report.Providers[0].Models[0].Err)
}

// authCheckingUpstream — 200 only for the exact Authorization header.
func authCheckingUpstream(t *testing.T, want string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad bearer"}}`))

			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"` + req.Model + `","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}
